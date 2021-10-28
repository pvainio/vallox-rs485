# Vallox RS485 serial interface module for Go lang

## Overview

This module implements Vallox RS485 serial protocol.  Currently it can:
- read temperature reported by device: outside -> incoming -> inside -> outgoing
- read ventilation fan speed

## Supported devices

The module has been tested with only one device so far:
- Vallox Digit SE model 3500 SE made in 2001

Probably it will support other Vallox models using RS485 serial bus for remote controllers.  It is possible that some registers have have changed through time since this device register does not match Vallox documentation.

Use at your own risk!  Vallox documentation especially warns that using incorrect registers or incorrect values.

## Usage 

## Example

```go
package main

import (
	"log"

	valloxrs485 "github.com/pvainio/vallox-rs485"
)

var registers = map[byte]string{
	valloxrs485.FanSpeed:            "Speed",
	valloxrs485.TempIncomingOutside: "Ouside temp",
	valloxrs485.TempIncomingInside:  "Incoming temp",
	valloxrs485.TempOutgoingInside:  "Inside temp",
	valloxrs485.TempOutgoingOutside: "Outgoing temp",
}

func main() {
	cfg := valloxrs485.Config{Device: "/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A901IPIR-if00-port0"}
	vallox, err := valloxrs485.Open(cfg)

	if err != nil {
		log.Fatalf("error opening Vallox device %s: %v", cfg.Device, err)
	}

	for {
		event := <-vallox.Events()

		if !vallox.ForMe(event) {
			// Do not handle values addressed for someone else in the same bus
			continue
		}

		if name, ok := registers[event.Register]; ok {
			log.Printf("Received new value %s = %d", name, event.Value)
		} else {
			log.Printf("Received unidentified value register %d = %d", event.Register, event.Value)
		}
	}
}
```
