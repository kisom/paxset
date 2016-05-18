#!/bin/sh

TARGET =	paxset
SYSTEMD_PATH =	/lib/systemd/system

all: $(TARGET)

$(TARGET): main.go
	go build

$(TARGET).conf:
	echo "Please copy $(TARGET).conf.example and edit it to suit your system."

install: $(TARGET) $(TARGET).service
	install -o root -g daemon -m 0700 $(TARGET).conf /etc/$(TARGET).conf
	install -o root -g daemon -m 0700 $(TARGET) /usr/sbin/$(TARGET)
	install -o root -g root -m 0644 $(TARGET).service $(SYSTEMD_PATH)/$(TARGET).service
	systemctl enable $(TARGET).service
	systemctl start $(TARGET).service

uninstall:
	rm -f /usr/sbin/$(TARGET)
	rm -f $(SYSTEMD_PATH)/$(TARGET).service
	rm -f /etc/$(TARGET).conf

