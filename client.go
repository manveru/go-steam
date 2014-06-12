package steam

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io/ioutil"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manveru/go-steam/cryptoutil"
	. "github.com/manveru/go-steam/internal"
	. "github.com/manveru/go-steam/steamid"
)

// Represents a client to the Steam network.
// Always poll events from the channel returned by Events() or receiving messages will stop.
// All access, unless otherwise noted, should be threadsafe.
//
// When a FatalError is emitted, the connection is automatically closed. The same client can be used to reconnect.
// Other errors don't have any effect.
type Client struct {
	Auth    *Auth
	Social  *Social
	Web     *Web
	Trading *Trading
	GC      *GameCoordinator

	sessionId int32
	steamId   uint64

	currentJobId uint64

	events   chan interface{}
	handlers []PacketHandler

	tempSessionKey []byte

	ConnectionTimeout time.Duration

	conn      connection
	connMutex sync.RWMutex
	writeChan chan IMsg
	heartbeat *time.Ticker
	address   string
}

type PacketHandler interface {
	HandlePacket(*PacketMsg)
}

func NewClient() *Client {
	client := &Client{
		events: make(chan interface{}, 3),
	}
	client.Auth = &Auth{client: client}
	client.RegisterPacketHandler(client.Auth)
	client.Social = newSocial(client)
	client.RegisterPacketHandler(client.Social)
	client.Web = &Web{client: client}
	client.RegisterPacketHandler(client.Web)
	client.Trading = &Trading{client: client}
	client.RegisterPacketHandler(client.Trading)
	client.GC = newGC(client)
	client.RegisterPacketHandler(client.GC)
	return client
}

// Get the event channel. By convention all events are pointers, except for errors.
func (c *Client) Events() <-chan interface{} {
	return c.events
}

func (c *Client) Emit(event interface{}) {
	c.events <- event
}

// When this error is emitted by the Client, the connection is automatically closed.
// This may be a network error, for example.
type FatalError error

// Emits a FatalError formatted with fmt.Errorf and disconnects.
func (c *Client) Fatalf(format string, a ...interface{}) {
	c.Emit(FatalError(fmt.Errorf(format, a...)))
	c.Disconnect()
}

// Emits an error formatted with fmt.Errorf.
func (c *Client) Errorf(format string, a ...interface{}) {
	c.Emit(fmt.Errorf(format, a...))
}

// Emits an info log event
func (c *Client) Infof(format string, a ...interface{}) {
	c.Emit(fmt.Sprintf(format, a...))
}

// Registers a PacketHandler that receives all incoming packets.
func (c *Client) RegisterPacketHandler(handler PacketHandler) {
	c.handlers = append(c.handlers, handler)
}

func (c *Client) GetNextJobId() JobId {
	return JobId(atomic.AddUint64(&c.currentJobId, 1))
}

func (c *Client) SteamId() SteamId {
	return SteamId(atomic.LoadUint64(&c.steamId))
}

func (c *Client) SessionId() int32 {
	return atomic.LoadInt32(&c.sessionId)
}

func (c *Client) Connected() bool {
	return (c.conn != nil)
}

// Connects to a random server of the Steam network and returns the server.
// If this client is already connected, it is disconnected first.
func (c *Client) Connect() string {
	server := getRandomCM()
	c.ConnectTo(server)
	return server
}

// Connects to a specific server.
// If this client is already connected, it is disconnected first.
func (c *Client) ConnectTo(address string) {
	c.connMutex.Lock()

	if c.conn != nil {
		c.Infof("won't connect to %s (already connected to %s)", address, c.address)
		c.connMutex.Unlock()
		return
	}

	c.Infof("connecting to %s...", address)

	conn, err := dialTCP(address)
	if err != nil {
		c.connMutex.Unlock()
		c.Fatalf("dial error: %v (transient)", err)
		return
	}

	c.address = address
	c.conn = conn
	c.writeChan = make(chan IMsg, 5)

	go c.readLoop()
	go c.writeLoop()

	c.Infof("connected to %s", address)
	c.connMutex.Unlock()
}

func (c *Client) Disconnect() {
	c.connMutex.Lock()
	defer c.connMutex.Unlock()

	if c.conn == nil {
		return
	}

	c.Infof("disconnecting from %s...", c.address)
	c.stopHeartbeatLoop()
	c.conn.Close()
	c.conn = nil
	c.Infof("disconnected from %s.", c.address)
}

// Adds a message to the send queue. Modifications to the given message after
// writing are not allowed (possible race conditions).
//
// Writes to this client when not connected are ignored.
func (c *Client) Write(msg IMsg) {
	if cm, ok := msg.(IClientMsg); ok {
		cm.SetSessionId(c.SessionId())
		cm.SetSteamId(c.SteamId())
	}
	c.writeChan <- msg
}

func (c *Client) readLoop() {
	var packet *PacketMsg
	var err error

	for {
		if c.conn == nil {
			c.Infof("read loop stopping (connection closed)")
			return
		}

		if packet, err = c.conn.Read(); err != nil {
			if c.conn != nil {
				c.Fatalf("read error: %s (transient)", err)
			}
			return
		}

		if packet == nil {
			c.Infof("got nil packet")
			<-time.After(500 * time.Millisecond)
		} else {
			c.handlePacket(packet)
		}
	}
}

func (c *Client) writeLoop() {
	var err error
	var buf *bytes.Buffer = new(bytes.Buffer)

	for msg := range c.writeChan {
		buf.Reset()

		if err = msg.Serialize(buf); err != nil {
			c.Errorf("error serializing message %v: %s", msg, err)
			return
		}

		if c.conn == nil {
			c.Infof("write loop stopping (connection closed)")
			return
		}

		if err = c.conn.Write(buf.Bytes()); err != nil {
			if c.conn != nil {
				c.Fatalf("write error: %s (transient)", err)
			}
			return
		}
	}
}

func (c *Client) heartbeatLoop(seconds time.Duration) {
	c.stopHeartbeatLoop()
	c.heartbeat = time.NewTicker(seconds * time.Second)

	defer c.stopHeartbeatLoop()

	for _ = range c.heartbeat.C {
		c.Write(NewClientMsgProtobuf(EMsg_ClientHeartBeat, new(CMsgClientHeartBeat)))
	}
}

func (c *Client) stopHeartbeatLoop() {
	if c.heartbeat == nil {
		return
	}
	c.heartbeat.Stop()
	c.heartbeat = nil
}

func (c *Client) handlePacket(packet *PacketMsg) {
	switch packet.EMsg {
	case EMsg_ChannelEncryptRequest:
		c.handleChannelEncryptRequest(packet)
	case EMsg_ChannelEncryptResult:
		c.handleChannelEncryptResult(packet)
	case EMsg_Multi:
		c.handleMulti(packet)
	case EMsg_ClientLoggedOff:
		c.clientLoggedOff(packet)
	case EMsg_ClientCMList:
		updateServerList(packet)
	}
	for _, handler := range c.handlers {
		handler.HandlePacket(packet)
	}
}

func (c *Client) handleChannelEncryptRequest(packet *PacketMsg) {
	body := NewMsgChannelEncryptRequest()
	packet.ReadMsg(body)

	if body.Universe != EUniverse_Public {
		c.Fatalf("invalid universe %v!", body.Universe)
	}

	c.tempSessionKey = make([]byte, 32)
	rand.Read(c.tempSessionKey)
	encryptedKey := cryptoutil.RSAEncrypt(GetPublicKey(EUniverse_Public), c.tempSessionKey)

	payload := new(bytes.Buffer)
	payload.Write(encryptedKey)
	binary.Write(payload, binary.LittleEndian, crc32.ChecksumIEEE(encryptedKey))
	payload.WriteByte(0)
	payload.WriteByte(0)
	payload.WriteByte(0)
	payload.WriteByte(0)

	c.Write(NewMsg(NewMsgChannelEncryptResponse(), payload.Bytes()))
}

type ConnectedEvent struct{}

func (c *Client) handleChannelEncryptResult(packet *PacketMsg) {
	body := NewMsgChannelEncryptResult()
	packet.ReadMsg(body)

	if body.Result != EResult_OK {
		c.Fatalf("encryption error: %v", body.Result)
		return
	}

	if c.conn != nil {
		c.conn.SetEncryptionKey(c.tempSessionKey)
	}

	c.tempSessionKey = nil

	c.Emit(new(ConnectedEvent))
}

func (c *Client) handleMulti(packet *PacketMsg) {
	body := new(CMsgMulti)
	packet.ReadProtoMsg(body)

	payload := body.GetMessageBody()

	if body.GetSizeUnzipped() > 0 {
		archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			panic(err)
		}

		for _, f := range archive.File {
			if f.Name == "z" {
				r, _ := f.Open()
				payload, _ = ioutil.ReadAll(r)
				goto okay
			}
		}

		c.Errorf("invalid multi packet %v: could not find 'z' file!", packet)
		return

	okay: // jump over error
	}

	pr := bytes.NewReader(payload)
	for pr.Len() > 0 {
		var length uint32
		binary.Read(pr, binary.LittleEndian, &length)
		packetData := make([]byte, length)
		pr.Read(packetData)
		p, err := NewPacketMsg(packetData)
		if err != nil {
			c.Errorf("error reading packet in multi msg %v: %v", packet, err)
			continue
		}
		c.handlePacket(p)
	}
}

func (c *Client) clientLoggedOff(packet *PacketMsg) {
	msg := &CMsgClientLoggedOff{}
	packet.ReadProtoMsg(msg)

	c.Errorf("client logged off: %s", EResult(msg.GetEresult()).String())
}
