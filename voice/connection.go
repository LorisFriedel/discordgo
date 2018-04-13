package voice

import (
	"sync"
	"net"
	"github.com/gorilla/websocket"
)

type Connection interface {
	websocketInterface
	udpInterface
}

// A ConnectionImpl struct holds all the data and functions related to a Discord Voice VoiceConnection.
type ConnectionImpl struct {
	sync.RWMutex

	Ready        bool // If true, voice is ready to send/receive audio
	UserID       string
	GuildID      string
	ChannelID    string
	deaf         bool
	mute         bool
	speaking     bool
	reconnecting bool // If true, voice connection is trying to reconnect

	OpusSend chan []byte  // Chan for sending opus audio
	OpusRecv chan *Packet // Chan for receiving opus audio

	wsConn  *websocket.Conn
	wsMutex *sync.Mutex
	udpConn *net.UDPConn

	// TODO rewrite
	sWsMutex          *sync.Mutex
	sWsConn           **websocket.Conn
	sVoiceConnections map[string]*Connection
	sMutex            *sync.Mutex

	sessionID string
	token     string
	endpoint  string

	// Used to send a close signal to goroutines
	close chan struct{}

	// Used to allow blocking until connected
	connected chan bool

	// Used to pass the sessionid from onVoiceStateUpdate
	// sessionRecv chan string UNUSED ATM

	op4 SessionDescriptionDataServerOp4
	op2 ReadyDataServerOp2

	speakingUpdateHandlers []SpeakingUpdateHandler
}

func NewConnection(wsMu *sync.Mutex, wsConn **websocket.Conn, voiceConnections map[string]*Connection, mu *sync.Mutex) *ConnectionImpl {
	return &ConnectionImpl{
		sWsMutex:          wsMu,
		sWsConn:           wsConn,
		sVoiceConnections: voiceConnections,
		sMutex:            mu,
	}
}

func (c *ConnectionImpl) Update(gID, cID string, mute, deaf bool) {
	c.Lock()
	defer c.Unlock()

	c.GuildID = gID
	c.ChannelID = cID
	c.deaf = deaf
	c.mute = mute
}

//TODO session wsMutex, session wsConn, session voiceConnections
