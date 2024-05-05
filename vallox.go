// Package valloxrs485 implements Vallox RS485 protocol
package valloxrs485

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"time"

	"github.com/tarm/serial"
)

// Config foo
type Config struct {
	// Device file for rs485 device
	Device string
	// RemoteClientId is the id for this device in Vallox rs485 bus
	RemoteClientId byte
	// Enable writing to Vallox regisers, default false
	EnableWrite bool
	// Logge for debug, default no logging
	LogDebug *log.Logger
}

type Vallox struct {
	port           *serial.Port
	remoteClientId byte
	running        bool
	buffer         *bufio.ReadWriter
	in             chan Event
	out            chan valloxPackage
	lastActivity   time.Time
	writeAllowed   bool
	logDebug       *log.Logger

	co2 twoByteValue
}

type twoByteValue struct {
	high byteValue
	low  byteValue
}

func (tbv *twoByteValue) validValue(now time.Time) (int16, bool) {
	limit := now.Add(-500 * time.Millisecond)
	if tbv.high.at.Before(limit) {
		return -1, false
	}
	if tbv.low.at.Before(limit) {
		return -1, false
	}
	// Both values are within 500ms of the current time
	res := int16(tbv.high.value)<<8 + int16(tbv.low.value)
	if res <= 0 {
		return -1, false
	}
	return res, true
}

type byteValue struct {
	at    time.Time
	value byte
}

const (
	DeviceMulticast       = 0x10
	DeviceMain            = 0x11
	RemoteClientMulticast = 0x20
)

// Some known registers
const (
	// Reading and writing fan speed
	FanSpeed byte = 0x29

	// Registers Vallox is broadcasting temperatures
	TempIncomingOutside byte = 0x58
	TempOutgoingInside  byte = 0x5a
	TempIncomingInside  byte = 0x5b
	TempOutgoingOutside byte = 0x5c

	// Registers for newer protocol
	TempIncomingOutsideNew byte = 0x32
	TempOutgoingInsideNew  byte = 0x34
	TempIncomingInsideNew  byte = 0x35
	TempOutgoingOutsideNew byte = 0x33

	RhHighest          byte = 0x2a
	Co2HighestHighByte byte = 0x2b
	Co2HighestLowByte  byte = 0x2c
	Rh1                byte = 0x2f
	Rh2                byte = 0x30
)

type Event struct {
	Time        time.Time `json:"time"`
	Source      byte      `json:"source"`
	Destination byte      `json:"destination"`
	Register    byte      `json:"register"`
	RawValue    byte      `json:"raw"`
	Value       int16     `json:"value"`
}

type valloxPackage struct {
	System      byte
	Source      byte
	Destination byte
	Register    byte
	Value       byte
	Checksum    byte
}

var writeAllowed = map[byte]bool{FanSpeed: true}

// Open opens the rs485 device specified in Config
func Open(cfg Config) (*Vallox, error) {

	if cfg.LogDebug == nil {
		cfg.LogDebug = log.New(ioutil.Discard, "", 0)
	}

	if cfg.RemoteClientId == 0 {
		cfg.RemoteClientId = 0x27
	}

	if cfg.RemoteClientId < 0x20 || cfg.RemoteClientId > 0x2f {
		return nil, fmt.Errorf("invalid remoteClientId %x", cfg.RemoteClientId)
	}

	portCfg := &serial.Config{Name: cfg.Device, Baud: 9600, Size: 8, Parity: 'N', StopBits: 1}
	port, err := serial.OpenPort(portCfg)
	if err != nil {
		return nil, err
	}

	buffer := new(bytes.Buffer)
	vallox := &Vallox{
		port:           port,
		running:        true,
		buffer:         bufio.NewReadWriter(bufio.NewReader(buffer), bufio.NewWriter(buffer)),
		remoteClientId: cfg.RemoteClientId,
		in:             make(chan Event, 50),
		out:            make(chan valloxPackage, 50),
		writeAllowed:   cfg.EnableWrite,
		logDebug:       cfg.LogDebug,
	}

	sendInit(vallox)

	go handleIncoming(vallox)
	go handleOutgoing(vallox)

	return vallox, nil
}

// Events returns channel for events from Vallox bus
func (vallox Vallox) Events() chan Event {
	return vallox.in
}

// ForMe returns true if event is addressed for this client
func (vallox Vallox) ForMe(e Event) bool {
	return e.Destination == RemoteClientMulticast || e.Destination == vallox.remoteClientId
}

// Query queries Vallox for register
func (vallox Vallox) Query(register byte) {
	pkg := createQuery(vallox, register)
	vallox.out <- *pkg
}

// SetSpeed changes speed of ventilation fan
func (vallox Vallox) SetSpeed(speed byte) {
	if speed < 1 || speed > 8 {
		vallox.logDebug.Printf("received invalid speed %x", speed)
		return
	}
	value := speedToValue(int8(speed))
	vallox.logDebug.Printf("received set speed %x", speed)
	// Send value to the main vallox device
	vallox.writeRegister(DeviceMain, FanSpeed, value)
	// Also publish value to all the remotes
	vallox.writeRegister(RemoteClientMulticast, FanSpeed, value)
}

func sendInit(vallox *Vallox) {
	vallox.Query(FanSpeed)
}

func (vallox Vallox) writeRegister(destination byte, register byte, value byte) {
	pkg := createWrite(vallox, destination, register, value)
	vallox.out <- *pkg
}

func createQuery(vallox Vallox, register byte) *valloxPackage {
	return createWrite(vallox, DeviceMain, 0, register)
}

func createWrite(vallox Vallox, destination byte, register byte, value byte) *valloxPackage {
	pkg := new(valloxPackage)
	pkg.System = 1
	pkg.Source = vallox.remoteClientId
	pkg.Destination = destination
	pkg.Register = register
	pkg.Value = value
	pkg.Checksum = calculateChecksum(pkg)
	return pkg
}

func handleOutgoing(vallox *Vallox) {
	for vallox.running {
		pkg := <-vallox.out

		if !isOutgoingAllowed(vallox, pkg.Register) {
			vallox.logDebug.Printf("outgoing not allowed for %x = %x", pkg.Register, pkg.Value)
			continue
		}

		now := time.Now()
		if vallox.lastActivity.IsZero() || now.UnixMilli()-vallox.lastActivity.UnixMilli() < 50 {
			updateLastActivity(vallox)
			vallox.logDebug.Printf("delay outgoing to %x %x = %x, lastActivity %v now %v, diff %d ms",
				pkg.Destination, pkg.Register, pkg.Value, vallox.lastActivity, now, now.UnixMilli()-vallox.lastActivity.UnixMilli())
			time.Sleep(time.Millisecond * 50)
			vallox.out <- pkg
		} else {
			updateLastActivity(vallox)
			binary.Write(vallox.port, binary.BigEndian, pkg)
			vallox.logDebug.Printf("sent outgoing to %x %x = %x", pkg.Destination, pkg.Register, pkg.Value)
		}
	}
}

func isOutgoingAllowed(vallox *Vallox, register byte) bool {
	if register == 0 {
		// queries are allowed
		return true
	}

	if !vallox.writeAllowed {
		return false
	}

	return writeAllowed[register]
}

func handleIncoming(vallox *Vallox) {
	vallox.running = true
	buf := make([]byte, 6)
	for vallox.running {
		n, err := vallox.port.Read(buf)
		if err != nil {
			fatalError(err, vallox)
			return
		}
		if n > 0 {
			updateLastActivity(vallox)
			vallox.buffer.Write(buf[:n])
			vallox.buffer.Writer.Flush()
			handleBuffer(vallox)
		}
	}
}

func updateLastActivity(vallox *Vallox) {
	vallox.lastActivity = time.Now()
}

func fatalError(err error, vallox *Vallox) {
	vallox.running = false
}

func handleBuffer(vallox *Vallox) {
	for {
		buf, err := vallox.buffer.Peek(6)
		if err != nil && err == io.EOF {
			// not enough bytes, ok, continue
			return
		} else if err != nil {
			fatalError(err, vallox)
			return
		}
		pkg := validPackage(buf)
		if pkg != nil {
			vallox.buffer.Discard(6)
			handlePackage(pkg, vallox)
		} else {
			// discard byte, since no valid package starts here
			vallox.buffer.ReadByte()
		}
	}
}

func handlePackage(pkg *valloxPackage, vallox *Vallox) {
	e := event(pkg, vallox)
	if e != nil {
		vallox.in <- *e
	} else {
		vallox.logDebug.Printf("discarding package from %d register %d value %d", pkg.Source, pkg.Register, pkg.Value)
	}
}

type mapFn func(byte, *Vallox) (int16, bool)

var registerMap = map[byte]mapFn{
	FanSpeed:               valueToSpeed,
	TempIncomingInside:     valueToTemp,
	TempIncomingOutside:    valueToTemp,
	TempOutgoingInside:     valueToTemp,
	TempOutgoingOutside:    valueToTemp,
	TempIncomingInsideNew:  valueToTemp,
	TempIncomingOutsideNew: valueToTemp,
	TempOutgoingInsideNew:  valueToTemp,
	TempOutgoingOutsideNew: valueToTemp,

	RhHighest:          valueToRh,
	Rh1:                valueToRh,
	Rh2:                valueToRh,
	Co2HighestHighByte: valueToCo2High,
	Co2HighestLowByte:  valueToCo2Low,
}

func valueToRh(val byte, vallox *Vallox) (int16, bool) {
	if val < 0x33 {
		return -1, false
	}
	return int16(math.Round(float64((float32(val) - 51.0) / 2.04))), true
}

func valueToCo2High(val byte, vallox *Vallox) (int16, bool) {
	now := time.Now()
	vallox.co2.high = byteValue{at: now, value: val}
	return vallox.co2.validValue(now)
}

func valueToCo2Low(val byte, vallox *Vallox) (int16, bool) {
	now := time.Now()
	vallox.co2.low = byteValue{at: now, value: val}
	return vallox.co2.validValue(now)
}

func event(pkg *valloxPackage, vallox *Vallox) *Event {
	event := new(Event)
	event.Time = time.Now()
	event.Source = pkg.Source
	event.Destination = pkg.Destination
	event.Register = pkg.Register
	event.RawValue = pkg.Value
	mapFn, found := registerMap[pkg.Register]
	if found {
		val, ok := mapFn(pkg.Value, vallox)
		if !ok {
			return nil
		}
		event.Value = int16(val)
	} else {
		event.Value = int16(pkg.Value)
	}
	return event
}

func valueToSpeed(value byte, vallox *Vallox) (int16, bool) {
	for i, v := range fanSpeedConversion {
		if value == v {
			return int16(i) + 1, true
		}
	}
	return -1, false
}

func speedToValue(speed int8) byte {
	return fanSpeedConversion[speed-1]
}

func valueToTemp(value byte, vallox *Vallox) (int16, bool) {
	return tempConversion[value], true
}

func validPackage(buffer []byte) (pkg *valloxPackage) {
	pkg = new(valloxPackage)
	err := binary.Read(bytes.NewReader(buffer), binary.LittleEndian, pkg)

	if err == nil && validChecksum(pkg) {
		return pkg
	}

	return nil
}

func validChecksum(pkg *valloxPackage) bool {
	return pkg.Checksum == calculateChecksum(pkg)
}

func calculateChecksum(pkg *valloxPackage) byte {
	return pkg.System + pkg.Source + pkg.Destination + pkg.Register + pkg.Value
}

var fanSpeedConversion = [8]byte{0x01, 0x03, 0x07, 0x0f, 0x1f, 0x3f, 0x7f, 0xff}

var tempConversion = [256]int16{
	-74, -70, -66, -62, -59, -56, -54, -52, -50, -48, -47, -46, -44, -43, -42, -41,
	-40, -39, -38, -37, -36, -35, -34, -33, -33, -32, -31, -30, -30, -29, -28, -28, -27, -27, -26, -25, -25,
	-24, -24, -23, -23, -22, -22, -21, -21, -20, -20, -19, -19, -19, -18, -18, -17, -17, -16, -16, -16, -15,
	-15, -14, -14, -14, -13, -13, -12, -12, -12, -11, -11, -11, -10, -10, -9, -9, -9, -8, -8, -8, -7, -7, -7,
	-6, -6, -6, -5, -5, -5, -4, -4, -4, -3, -3, -3, -2, -2, -2, -1, -1, -1, -1, 0, 0, 0, 1, 1, 1, 2, 2, 2, 3, 3,
	3, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 7, 7, 7, 8, 8, 8, 9, 9, 9, 10, 10, 10, 11, 11, 11, 12, 12, 12, 13, 13, 13,
	14, 14, 14, 15, 15, 15, 16, 16, 16, 17, 17, 18, 18, 18, 19, 19, 19, 20, 20, 21, 21, 21, 22, 22, 22, 23, 23,
	24, 24, 24, 25, 25, 26, 26, 27, 27, 27, 28, 28, 29, 29, 30, 30, 31, 31, 32, 32, 33, 33, 34, 34, 35, 35, 36,
	36, 37, 37, 38, 38, 39, 40, 40, 41, 41, 42, 43, 43, 44, 45, 45, 46, 47, 48, 48, 49, 50, 51, 52, 53, 53, 54,
	55, 56, 57, 59, 60, 61, 62, 63, 65, 66, 68, 69, 71, 73, 75, 77, 79, 81, 82, 86, 90, 93, 97, 100, 100, 100,
	100, 100, 100, 100, 100, 100,
}
