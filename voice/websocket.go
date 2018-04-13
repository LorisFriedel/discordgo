package voice

import (
	"fmt"
	"time"
	"strings"
	"encoding/json"

	"github.com/gorilla/websocket"

	log "github.com/sirupsen/logrus"
)

type websocketInterface interface {
	Open() (err error)

	Speaking(b bool) (err error)
	ChangeChannel(channelID string, mute, deaf bool) (err error)
	Disconnect() (err error)
	Close()
	AddHandler(h SpeakingUpdateHandler)

	waitUntilConnected() error
	wsListen(wsConn *websocket.Conn, close <-chan struct{})
	onEvent(message []byte)
	wsHeartbeat(wsConn *websocket.Conn, close <-chan struct{}, period time.Duration)
}

// SpeakingUpdateHandler type provides a function definition for the SpeakingUpdate event
type SpeakingUpdateHandler func(vc *ConnectionImpl, vs *SpeakingDataServerOp5)

// Opcode 2 - SERVER
type ReadyDataServerOp2 struct {
	SSRC              uint32        `json:"ssrc"`
	Port              int           `json:"port"`
	Modes             []string      `json:"modes"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

// Opcode 3 - CLIENT
type HeartbeatClientOp3 struct {
	Op   int `json:"op"` // Always 3
	Data int `json:"d"`
}

// Opcode 4 - CLIENT
type ChannelJoinClientOp4 struct {
	Op   int                      `json:"op"` // Always 4
	Data ChannelJoinDataClientOp4 `json:"d"`
}

type ChannelJoinDataClientOp4 struct {
	GuildID   *string `json:"guild_id"`
	ChannelID *string `json:"channel_id"`
	SelfMute  bool    `json:"self_mute"`
	SelfDeaf  bool    `json:"self_deaf"`
}

// Opcode 4 - SERVER
type SessionDescriptionDataServerOp4 struct {
	SecretKey [32]byte `json:"secret_key"`
	Mode      string   `json:"mode"`
}

// Opcode 5 - CLIENT
type SpeakingClientOp5 struct {
	Op   int                   `json:"op"` // Always 5
	Data SpeakingDataClientOp5 `json:"d"`
}

type SpeakingDataClientOp5 struct {
	Speaking bool `json:"speaking"`
	Delay    int  `json:"delay"`
}

// Opcode 5 - SERVER
type SpeakingDataServerOp5 struct {
	UserID   string `json:"user_id"`
	SSRC     int    `json:"ssrc"`
	Speaking bool   `json:"speaking"`
}

// Opcode 8 - SERVER
type HelloDataServerOp8 struct {
	HeartbeatInterval int64 `json:"heartbeat_interval"` // in milliseconds (ms)
}

// Event provides a basic initial struct for all websocket events.
type Event struct {
	Opcode   Opcode          `json:"op"`
	Sequence int64           `json:"s"`
	Type     string          `json:"t"`
	RawData  json.RawMessage `json:"d"`
	// Struct contains one of the other types in this file.
	Struct interface{} `json:"-"`
}

// Speaking sends a speaking notification to Discord over the voice websocket.
// This must be sent as true prior to sending audio and should be set to false
// once finished sending audio.
//  b  : Send true if speaking, false if not.
func (c *ConnectionImpl) Speaking(b bool) (err error) {

	log.Debugf("called (%t)", b)

	if c.wsConn == nil {
		return fmt.Errorf("no websocket connection")
	}

	data := SpeakingClientOp5{5, SpeakingDataClientOp5{b, 0}}
	c.wsMutex.Lock()
	err = c.wsConn.WriteJSON(data)
	c.wsMutex.Unlock()

	c.Lock()
	defer c.Unlock()
	if err != nil {
		c.speaking = false
		log.Errorf("Speaking() write json error:", err)
		return
	}

	c.speaking = b

	return
}

// ChangeChannel sends Discord a request to change channels within a Guild
// !!! NOTE !!! This function may be removed in favour of just using ChannelVoiceJoin
func (c *ConnectionImpl) ChangeChannel(channelID string, mute, deaf bool) (err error) {

	log.Debug("called")

	data := ChannelJoinClientOp4{4, ChannelJoinDataClientOp4{&c.GuildID, &channelID, mute, deaf}}
	c.wsMutex.Lock()
	err = c.sWsConn.WriteJSON(data)
	c.wsMutex.Unlock()
	if err != nil {
		return
	}
	c.ChannelID = channelID
	c.deaf = deaf
	c.mute = mute
	c.speaking = false

	return
}

// Disconnect disconnects from this voice channel and closes the websocket
// and udp connections to Discord.
// !!! NOTE !!! this function may be removed in favour of ChannelVoiceLeave
func (c *ConnectionImpl) Disconnect() (err error) {

	// Send a OP4 with a nil channel to disconnect
	if c.sessionID != "" {
		data := ChannelJoinClientOp4{4, ChannelJoinDataClientOp4{&c.GuildID, nil, true, true}} // TODO
		c.sWsMutex.Lock()
		err = c.sWsConn.WriteJSON(data)
		c.sWsMutex.Unlock()
		c.sessionID = ""
	}

	// Close websocket and udp connections
	c.Close()

	log.Debugf("deleting voice.ConnectionImpl %s", c.GuildID)

	c.sMutex.Lock()
	delete(c.sVoiceConnections, c.GuildID) // TODO
	c.sMutex.Unlock()

	return
}

// Close closes the voice ws and udp connections
func (c *ConnectionImpl) Close() {

	log.Debug("called")

	c.Lock()
	defer c.Unlock()

	c.Ready = false
	c.speaking = false

	if c.close != nil {
		log.Debug("closing v.close")
		close(c.close)
		c.close = nil
	}

	if c.udpConn != nil {
		log.Debug("closing udp")
		err := c.udpConn.Close()
		if err != nil {
			log.Errorf("error closing udp connection: ", err)
		}
		c.udpConn = nil
	}

	if c.wsConn != nil {
		log.Debug("sending close frame")

		// To cleanly close a connection, a client should send a close
		// frame and wait for the server to close the connection.
		c.wsMutex.Lock()
		err := c.wsConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.wsMutex.Unlock()
		if err != nil {
			log.Errorf("error closing websocket, %s", err)
		}

		// TODO: Wait for Discord to actually close the connection.
		time.Sleep(1 * time.Second)

		log.Debug("closing websocket")
		err = c.wsConn.Close()
		if err != nil {
			log.Errorf("error closing websocket, %s", err)
		}

		c.wsConn = nil
	}
}

// AddHandler adds a Handler for VoiceSpeakingUpdate events.
func (c *ConnectionImpl) AddHandler(h SpeakingUpdateHandler) {
	c.Lock()
	defer c.Unlock()

	c.speakingUpdateHandlers = append(c.speakingUpdateHandlers, h)
}

// ------------------------------------------------------------------------------------------------
// Unexported Internal Functions Below.
// ------------------------------------------------------------------------------------------------

// WaitUntilConnected waits for the Voice VoiceConnection to
// become ready, if it does not become ready it returns an err
func (c *ConnectionImpl) waitUntilConnected() error { // TODO not export

	log.Infof("called")

	i := 0
	for {
		c.RLock()
		ready := c.Ready
		c.RUnlock()
		if ready {
			return nil
		}

		if i > 20 {
			return fmt.Errorf("timeout waiting for voice")
		}

		time.Sleep(500 * time.Millisecond)
		i++
	}
}

// Open opens a voice connection.  This should be called
// after VoiceChannelJoin is used and the data VOICE websocket events
// are captured.
func (c *ConnectionImpl) Open() (err error) { // TODO not export

	log.Infof("called")

	c.Lock()
	defer c.Unlock()

	// Don't open a websocket if one is already open
	if c.wsConn != nil {
		log.Warnf("websocket already open, refusing to overwrite non-nil websocket")
		return
	}

	// TODO temp? loop to wait for the SessionID
	i := 0
	for {
		if c.sessionID != "" {
			break
		}
		if i > 20 { // only loop for up to 1 second total
			return fmt.Errorf("did not receive voice Session ID in time")
		}
		time.Sleep(50 * time.Millisecond)
		i++
	}

	// Connect to VoiceConnection Websocket
	vg := fmt.Sprintf("wss://%s", strings.TrimSuffix(c.endpoint, ":80"))
	log.Infof("connecting to voice endpoint %s", vg)
	c.wsConn, _, err = websocket.DefaultDialer.Dial(vg, nil)
	if err != nil {
		log.Warnf("error connecting to voice endpoint %s, %s", vg, err)
		log.Debugf("voice struct: %#v\n", c)
		return
	}

	type voiceHandshakeData struct {
		ServerID  string `json:"server_id"`
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
		Token     string `json:"token"`
	}
	type voiceHandshakeOp0 struct {
		Op   int                `json:"op"` // Always 0
		Data voiceHandshakeData `json:"d"`
	}
	data := voiceHandshakeOp0{0, voiceHandshakeData{c.GuildID, c.UserID, c.sessionID, c.token}}

	err = c.wsConn.WriteJSON(data)
	if err != nil {
		log.Warnf("error sending init packet, %s", err)
		return
	}

	log.Debugf("start listening on voice connection")

	c.close = make(chan struct{})
	go c.wsListen(c.wsConn, c.close)

	// add loop/check for Ready bool here?
	// then return false if not ready?
	// but then wsListen will also err.

	return
}

// wsListen listens on the voice websocket for messages and passes them
// to the voice event handler.  This is automatically called by the Open func
func (c *ConnectionImpl) wsListen(wsConn *websocket.Conn, close <-chan struct{}) {

	log.Debug("called")

	for {
		_, message, err := c.wsConn.ReadMessage()
		if err != nil {
			// Detect if we have been closed manually. If a Close() has already
			// happened, the websocket we are listening on will be different to the
			// current session.
			c.RLock()
			sameConnection := c.wsConn == wsConn
			c.RUnlock()
			if sameConnection {
				log.Errorf("voice endpoint %s websocket closed unexpectantly, %s", c.endpoint, err)

				go c.reconnect() // Start reconnect goroutine then exit.
			}
			return
		}

		// Pass received message to voice event handler
		select {
		case <-close:
			return
		default:
			go c.onEvent(message)
		}
	}
}

// wsEvent handles any voice websocket events. This is only called by the
// wsListen() function.
func (c *ConnectionImpl) onEvent(message []byte) {

	log.Debugf("UDP: received event: %s", string(message))

	var e Event
	err := json.Unmarshal(message, &e)
	if err != nil {
		log.Errorf("unmarshall error, %s", err)
		return
	}

	switch e.Opcode {

	case Ready: // READY
		if err := json.Unmarshal(e.RawData, &c.op2); err != nil {
			log.Errorf("OP2 unmarshall error, %s, %s", err, string(e.RawData))
			return
		}

		// Start the UDP connection
		err := c.udpOpen()
		if err != nil {
			log.Errorf("error opening udp connection, %s", err)
			return
		}

		// Start the opusSender.
		// TODO: Should we allow 48000/960 values to be user defined? !!!!!!!
		if c.OpusSend == nil {
			c.OpusSend = make(chan []byte, 2)
		}
		go c.opusSender(c.udpConn, c.close, c.OpusSend, 48000, 960)

		// Start the opusReceiver
		if !c.deaf {
			if c.OpusRecv == nil {
				c.OpusRecv = make(chan *Packet, 2)
			}

			go c.opusReceiver(c.udpConn, c.close, c.OpusRecv)
		}

		return

	case Heartbeat: // HEARTBEAT response
		// TODO
		// add code to use this to track latency?
		return

	case SessionDescription: // udp encryption secret key
		c.Lock()
		defer c.Unlock()

		c.op4 = SessionDescriptionDataServerOp4{}
		if err := json.Unmarshal(e.RawData, &c.op4); err != nil {
			log.Errorf("OP4 unmarshall error, %s, %s", err, string(e.RawData))
			return
		}
		return

	case Speaking:
		if len(c.speakingUpdateHandlers) == 0 {
			return
		}

		voiceSpeakingUpdate := &SpeakingDataServerOp5{}
		if err := json.Unmarshal(e.RawData, voiceSpeakingUpdate); err != nil {
			log.Errorf("OP5 unmarshall error, %s, %s", err, string(e.RawData))
			return
		}

		for _, h := range c.speakingUpdateHandlers {
			h(c, voiceSpeakingUpdate)
		}

	case Hello:
		var helloData HelloDataServerOp8
		if err := json.Unmarshal(e.RawData, &helloData); err != nil {
			log.Errorf("OP8 unmarshall error, %s, %s", err, string(e.RawData))
			return
		}

		log.Debugf("heartbeat bugged for voice websocket: %v ms", helloData.HeartbeatInterval)
		heartbeat := time.Duration(int(0.75*float64(helloData.HeartbeatInterval))) * time.Millisecond
		// * 0.75 temporary fix cf https://discordapp.com/developers/docs/topics/voice-connections#establishing-a-voice-udp-connection
		log.Debugf("heartbeat fixed for voice websocket: %v", heartbeat)

		// Start the voice websocket heartbeat to keep the connection alive
		go c.wsHeartbeat(c.wsConn, c.close, heartbeat)
		// TODO monitor a chan/bool to verify this was successful

	default:
		log.Debugf("unknown voice operation, opcode: %d, %s", e.Opcode, string(e.RawData))
	}

	return
}

// NOTE :: When a guild voice server changes how do we shut this down
// properly, so a new connection can be setup without fuss?
//
// wsHeartbeat sends regular heartbeats to voice Discord so it knows the client
// is still connected.  If you do not send these heartbeats Discord will
// disconnect the websocket connection after a few seconds.
func (c *ConnectionImpl) wsHeartbeat(wsConn *websocket.Conn, close <-chan struct{}, period time.Duration) {

	if close == nil || wsConn == nil {
		return
	}

	var err error
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		log.Debug("sending heartbeat packet")
		c.wsMutex.Lock()
		err = wsConn.WriteJSON(HeartbeatClientOp3{3, int(time.Now().Unix())})
		c.wsMutex.Unlock()
		if err != nil {
			log.Errorf("error sending heartbeat to voice endpoint %s, %s", c.endpoint, err)
			return
		}

		select {
		case <-ticker.C:
			// continue loop and send heartbeat
		case <-close:
			return
		}
	}
}
