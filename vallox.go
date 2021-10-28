package valloxrs485

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/tarm/serial"
)

type Config struct {
	Device         string
	RemoteClientId byte
}

type Vallox struct {
	port           *serial.Port
	remoteClientId byte
	running        bool
	buffer         *bufio.ReadWriter
	in             chan Event
	out            chan valloxPackage
	lastActivity   time.Time
}

const (
	DeviceMulticast       = 0x10
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

func Open(cfg Config) (*Vallox, error) {
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
		in:             make(chan Event, 15),
		out:            make(chan valloxPackage, 15),
	}

	sendInit(vallox)

	go handleIncoming(vallox)
	go handleOutgoing(vallox)

	return vallox, nil
}

func (vallox Vallox) Events() chan Event {
	return vallox.in
}

func (vallox Vallox) ForMe(e Event) bool {
	return e.Destination == RemoteClientMulticast || e.Destination == vallox.remoteClientId
}

func (vallox Vallox) Query(register byte) {
	pkg := createQuery(vallox, register)
	vallox.out <- *pkg
}

func sendInit(vallox *Vallox) {
	vallox.Query(FanSpeed)
}

func createQuery(vallox Vallox, register byte) *valloxPackage {
	pkg := new(valloxPackage)
	pkg.System = 1
	pkg.Source = vallox.remoteClientId
	pkg.Destination = 0x11
	pkg.Register = 0
	pkg.Value = register
	pkg.Checksum = calculateChecksum(pkg)
	return pkg
}

func handleOutgoing(vallox *Vallox) {
	for vallox.running {
		pkg := <-vallox.out
		now := time.Now()
		if vallox.lastActivity.IsZero() || now.UnixMilli()-vallox.lastActivity.UnixMilli() < 100 {
			time.Sleep(time.Millisecond * 100)
			vallox.out <- pkg
		} else {
			updateLastActivity(vallox)
			binary.Write(vallox.port, binary.BigEndian, pkg)
		}
	}
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
	vallox.in <- *event(pkg, vallox)
}

func event(pkg *valloxPackage, vallox *Vallox) *Event {
	event := new(Event)
	event.Time = time.Now()
	event.Source = pkg.Source
	event.Destination = pkg.Destination
	event.Register = pkg.Register
	event.RawValue = pkg.Value
	switch pkg.Register {
	case FanSpeed:
		event.Value = int16(valueToSpeed(pkg.Value))
	case TempIncomingInside:
		event.Value = int16(valueToTemp(pkg.Value))
	case TempIncomingOutside:
		event.Value = int16(valueToTemp(pkg.Value))
	case TempOutgoingInside:
		event.Value = int16(valueToTemp(pkg.Value))
	case TempOutgoingOutside:
		event.Value = int16(valueToTemp(pkg.Value))
	default:
		event.Value = int16(pkg.Value)
	}
	return event
}

func valueToSpeed(value byte) int8 {
	for i, v := range fanSpeedConversion {
		if value == v {
			return int8(i) + 1
		}
	}
	return -1
}

func valueToTemp(value byte) int8 {
	return tempConversion[value]
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

var tempConversion = [256]int8{
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
