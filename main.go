package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kisom/goutils/lib"
	"github.com/kisom/paxset/inotify"
)

var paxctl string

func runPaxctl(p program) error {
	flags := "-c" + p.Flags
	cmd := exec.Command(paxctl, flags, p.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

type program struct {
	Path  string
	Flags string
}

type config struct {
	// Path to the configuration file. In dæmon mode, this will be
	// watched for changes and automagically reloaded.
	Path string

	// Progs is the list of programs to watch.
	Progs map[string]program
}

func loadConfig(path string) (*config, error) {
	conf := &config{Path: path, Progs: map[string]program{}}
	file, err := os.Open(conf.Path)
	if err != nil {
		return nil, err
	}

	defer file.Close()

	scn := bufio.NewScanner(file)
	for scn.Scan() {
		line := strings.TrimSpace(scn.Text())

		// Skip empty lines and commented lines.
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}

		ss := strings.Split(line, "\t")

		if len(ss) != 2 {
			return nil, errors.New("paxset: malformed line (expected path\tflags)")
		}

		conf.Progs[ss[0]] = program{Path: ss[0], Flags: ss[1]}
	}

	return conf, nil
}

func setFlags(c *config, p string) error {
	prog, ok := c.Progs[p]
	if !ok {
		return errors.New("paxset: invalid program")
	}

	return runPaxctl(prog)
}

func setAllFlags(c *config) error {
	errCount := 0
	for _, prog := range c.Progs {
		if err := runPaxctl(prog); err != nil {
			errCount++
		}
	}

	if errCount > 0 {
		return errors.New("paxset: failed to set all flags")
	}
	return nil
}

func checkMask(mask, fl uint32, desc string, n int, buf *bytes.Buffer) int {
	if mask&fl == 0 {
		return n
	}

	if n > 0 {
		buf.WriteString(", ")
	}

	n++
	buf.WriteString(desc)
	return n
}

func eventString(mask uint32) string {
	n := 0
	buf := &bytes.Buffer{}
	cm := func(fl uint32, desc string) int {
		return checkMask(mask, fl, desc, n, buf)
	}

	n = cm(inotify.IN_MODIFY, "modified")
	n = cm(inotify.IN_ATTRIB, "attrib")
	n = cm(inotify.IN_CREATE, "create")
	n = cm(inotify.IN_MOVE_SELF, "move_self")
	n = cm(inotify.IN_DELETE_SELF, "delete_self")
	cm(inotify.IN_CLOSE_WRITE, "close_write")
	return buf.String()
}

const wfl = inotify.IN_MODIFY | inotify.IN_ATTRIB | inotify.IN_CREATE |
	inotify.IN_MOVE_SELF | inotify.IN_DELETE_SELF | inotify.IN_CLOSE_WRITE

var last *inotify.Event

func watchEvent(c *config, w *inotify.Watcher) (bool, error) {
	select {
	case ev := <-w.Event:
		log.Printf("inotify event for %s (%s)", ev.Name, eventString(ev.Mask))
		if ev.Name == c.Path {
			return true, nil
		}

		if last == nil {
			last = ev
		} else {
			if last.Mask == ev.Mask && last.Name == ev.Name &&
				last.Cookie == ev.Cookie {
				return false, nil
			}
			last = ev
		}

		prog, ok := c.Progs[ev.Name]
		if !ok {
			// If an event is generated for a program that
			// isn't being watched, note this fact and carry on.
			log.Printf("inotify event for %s, but it isn't being monitored", ev.Name)
			return false, nil
		}

		_, err := os.Stat(prog.Path)
		if err != nil {
			// This case can happen if a program is removed;
			// this isn't an error, but the stat error should
			// be noted.
			log.Printf("unable to stat %s: %s", prog.Path, err)
			return false, nil
		}

		err = runPaxctl(prog)
		if err != nil {
			log.Printf("error running paxctl: %s", err)
			return false, err
		}
		return false, nil
	case err := <-w.Error:
		log.Printf("inotify returned an error: %s", err)
		return false, err
	}
}

func watchConfig(c *config) error {
	watcher, err := inotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch config file for changes.
	err = watcher.AddWatch(c.Path, inotify.IN_MODIFY|inotify.IN_MOVE)
	if err != nil {
		return err
	}

	// Watch the set of programs in the config.
	for _, prog := range c.Progs {
		log.Printf("adding watch for %s", prog.Path)
		if err = watcher.AddWatch(prog.Path, wfl); err != nil {
			return err
		}
	}

	var reload bool
	for {
		reload, err = watchEvent(c, watcher)
		if reload || err != nil {
			break
		}
	}

	return err
}

func main() {
	var confFile string
	var watcher bool

	flag.StringVar(&confFile, "c", "/etc/paxset.conf", "`path` to config file")
	flag.BoolVar(&watcher, "w", false, "run in watch mode")
	flag.Parse()

	var err error
	paxctl, err = exec.LookPath("paxctl")
	if err != nil {
		log.Fatalf("can't find paxctl: %s", err)
	}

	c, err := loadConfig(confFile)
	if err != nil {
		log.Fatalf("couldn't load config file: %s", err)
	}

	err = setAllFlags(c)
	if err != nil && !watcher {
		os.Exit(lib.ExitFailure)
	}

	if !watcher {
		os.Exit(lib.ExitSuccess)
	}

	for {
		err = watchConfig(c)
		if err != nil {
			<-time.After(time.Minute)
			continue
		}

		log.Printf("reloading config file from %s", c.Path)
		c, err = loadConfig(c.Path)
		if err != nil {
			log.Fatalf("failed to read config file: %s", err)
		}

		err = setAllFlags(c)
		if err != nil {
			log.Printf("%s", err)
		}
	}
}
