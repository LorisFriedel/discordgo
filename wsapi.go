// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains low level functions for interacting with the Discord
// data websocket interface.

package discordgo

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"github.com/LorisFriedel/discordgo/endpoint"
	"github.com/LorisFriedel/discordgo/voice"
	"sync"
)

// ErrWSAlreadyOpen is thrown when you attempt to open
// a websocket that already is open.
var ErrWSAlreadyOpen = errors.New("web socket already opened")

// ErrWSNotFound is thrown when you attempt to use a websocket
// that doesn't exist
var ErrWSNotFound = errors.New("no websocket connection exists")

// ErrWSShardBounds is thrown when you try to use a shard ID that is
// less than the total shard count
var ErrWSShardBounds = errors.New("ShardID must be less than ShardCount")

type resumePacket struct {
	Op int `json:"op"`
	Data struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Sequence  int64  `json:"seq"`
	} `json:"d"`
}

// Open creates a websocket connection to Discord.
// See: https://discordapp.com/developers/docs/topics/gateway#connecting
func (s *Session) Open() error {
	log.Infof("called")

	var err error

	// Prevent Open or other major Session functions from
	// being called while Open is still running.
	s.Lock()
	defer s.Unlock()

	// If the websock is already open, bail out here.
	if s.wsConn != nil {
		return ErrWSAlreadyOpen
	}

	// Get the gateway to use for the Websocket connection
	if s.gateway == "" {
		s.gateway, err = s.Gateway()
		if err != nil {
			return err
		}

		// Add the version and encoding to the URL
		s.gateway = s.gateway + "?v=" + endpoint.APIVersion + "&encoding=json"
	}

	// Connect to the Gateway
	log.Infof("connecting to gateway %s", s.gateway)
	header := http.Header{}
	header.Add("accept-encoding", "zlib")
	s.wsConn, _, err = websocket.DefaultDialer.Dial(s.gateway, header)
	if err != nil {
		log.Warnf("error connecting to gateway %s, %s", s.gateway, err)
		s.gateway = "" // clear cached gateway
		s.wsConn = nil // Just to be safe.
		return err
	}

	defer func() {
		// because of this, all code below must set err to the error
		// when exiting with an error :)  Maybe someone has a better
		// way :)
		if err != nil {
			s.wsConn.Close()
			s.wsConn = nil
		}
	}()

	// The first response from Discord should be an Op 10 (Hello) Packet.
	// When processed by onEvent the heartbeat goroutine will be started.
	mt, m, err := s.wsConn.ReadMessage()
	if err != nil {
		return err
	}
	e, err := s.onEvent(mt, m)
	if err != nil {
		return err
	}
	if e.Opcode != 10 { // TODO
		err = fmt.Errorf("expecting Op 10, got Op %d instead", e.Opcode)
		return err
	}
	log.Infof("Op 10 Hello Packet received from Discord")
	s.LastHeartbeatAck = time.Now().UTC()
	var h helloOp // TODO
	if err = json.Unmarshal(e.RawData, &h); err != nil {
		err = fmt.Errorf("error unmarshalling helloOp, %s", err)
		return err
	}

	// Now we send either an Op 2 Identity if this is a brand new
	// connection or Op 6 Resume if we are resuming an existing connection.
	sequence := atomic.LoadInt64(s.sequence)
	if s.sessionID == "" && sequence == 0 {

		// Send Op 2 Identity Packet
		err = s.identify()
		if err != nil {
			err = fmt.Errorf("error sending identify packet to gateway, %s, %s", s.gateway, err)
			return err
		}

	} else {

		// Send Op 6 Resume Packet
		p := resumePacket{}
		p.Op = 6 // TODO
		p.Data.Token = s.Token
		p.Data.SessionID = s.sessionID
		p.Data.Sequence = sequence

		log.Infof("sending resume packet to gateway")
		s.wsMutex.Lock()
		err = s.wsConn.WriteJSON(p)
		s.wsMutex.Unlock()
		if err != nil {
			err = fmt.Errorf("error sending gateway resume packet, %s, %s", s.gateway, err)
			return err
		}

	}

	// A basic state is a hard requirement for Voice.
	// We create it here so the below READY/RESUMED packet can populate
	// the state :)
	// XXX: Move to New() func?
	if s.State == nil {
		state := NewState()
		state.TrackChannels = false
		state.TrackEmojis = false
		state.TrackMembers = false
		state.TrackRoles = false
		state.TrackVoice = false
		s.State = state
	}

	// Now Discord should send us a READY or RESUMED packet.
	mt, m, err = s.wsConn.ReadMessage()
	if err != nil {
		return err
	}
	e, err = s.onEvent(mt, m)
	if err != nil {
		return err
	}
	if e.Type != `READY` && e.Type != `RESUMED` { // TODO
		// This is not fatal, but it does not follow their API documentation.
		log.Warnf("Expected READY/RESUMED, instead got:\n%#v\n", e)
	}
	log.Infof("First Packet:\n%#v\n", e)

	log.Infof("We are now connected to Discord, emitting connect event")
	s.handleEvent(connectEventType, &Connect{})

	// A VoiceConnections map is a hard requirement for Voice.
	// XXX: can this be moved to when opening a voice connection?
	if s.VoiceConnections == nil {
		log.Infof("creating new VoiceConnections map")
		s.VoiceConnections = make(map[string]*VoiceConnection)
	}

	// Create listening chan outside of listen, as it needs to happen inside the
	// mutex lock and needs to exist before calling heartbeat and listen
	// go rountines.
	s.listening = make(chan interface{})

	// Start sending heartbeats and reading messages from Discord.
	go s.heartbeat(s.wsConn, s.listening, h.HeartbeatInterval)
	go s.listen(s.wsConn, s.listening)

	log.Infof("exiting")
	return nil
}

// listen polls the websocket connection for events, it will stop when the
// listening channel is closed, or an error occurs.
func (s *Session) listen(wsConn *websocket.Conn, listening <-chan interface{}) {

	log.Infof("called")

	for {

		messageType, message, err := wsConn.ReadMessage()

		if err != nil {

			// Detect if we have been closed manually. If a Close() has already
			// happened, the websocket we are listening on will be different to
			// the current session.
			s.RLock()
			sameConnection := s.wsConn == wsConn
			s.RUnlock()

			if sameConnection {

				log.Warnf("error reading from gateway %s websocket, %s", s.gateway, err)
				// There has been an error reading, close the websocket so that
				// OnDisconnect event is emitted.
				err := s.Close()
				if err != nil {
					log.Warnf("error closing session connection, %s", err)
				}

				log.Infof("calling reconnect() now")
				s.reconnect()
			}

			return
		}

		select {

		case <-listening:
			return

		default:
			s.onEvent(messageType, message)

		}
	}
}

type heartbeatOp struct {
	Op   int   `json:"op"`
	Data int64 `json:"d"`
}

type helloOp struct {
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
	Trace             []string      `json:"_trace"`
}

// FailedHeartbeatAcks is the Number of heartbeat intervals to wait until forcing a connection restart.
const FailedHeartbeatAcks time.Duration = 5 * time.Millisecond

// heartbeat sends regular heartbeats to Discord so it knows the client
// is still connected.  If you do not send these heartbeats Discord will
// disconnect the websocket connection after a few seconds.
func (s *Session) heartbeat(wsConn *websocket.Conn, listening <-chan interface{}, heartbeatIntervalMsec time.Duration) {

	log.Infof("called")

	if listening == nil || wsConn == nil {
		return
	}

	var err error
	ticker := time.NewTicker(heartbeatIntervalMsec * time.Millisecond)
	defer ticker.Stop()

	for {
		s.RLock()
		last := s.LastHeartbeatAck
		s.RUnlock()
		sequence := atomic.LoadInt64(s.sequence)
		log.Infof("sending gateway websocket heartbeat seq %d", sequence)
		s.wsMutex.Lock()
		err = wsConn.WriteJSON(heartbeatOp{1, sequence})
		s.wsMutex.Unlock()
		if err != nil || time.Now().UTC().Sub(last) > (heartbeatIntervalMsec*FailedHeartbeatAcks) {
			if err != nil {
				log.Errorf("error sending heartbeat to gateway %s, %s", s.gateway, err)
			} else {
				log.Errorf("haven't gotten a heartbeat ACK in %v, triggering a reconnection", time.Now().UTC().Sub(last))
			}
			s.Close()
			s.reconnect()
			return
		}
		s.Lock()
		s.DataReady = true
		s.Unlock()

		select {
		case <-ticker.C:
			// continue loop and send heartbeat
		case <-listening:
			return
		}
	}
}

// UpdateStatusData ia provided to UpdateStatusComplex()
type UpdateStatusData struct {
	IdleSince *int   `json:"since"`
	Game      *Game  `json:"game"`
	AFK       bool   `json:"afk"`
	Status    string `json:"status"`
}

type updateStatusOp struct {
	Op   int              `json:"op"`
	Data UpdateStatusData `json:"d"`
}

func newUpdateStatusData(idle int, gameType GameType, game, url string) *UpdateStatusData {
	usd := &UpdateStatusData{
		Status: "online",
	}

	if idle > 0 {
		usd.IdleSince = &idle
	}

	if game != "" {
		usd.Game = &Game{
			Name: game,
			Type: gameType,
			URL:  url,
		}
	}

	return usd
}

// UpdateStatus is used to update the user's status.
// If idle>0 then set status to idle.
// If game!="" then set game.
// if otherwise, set status to active, and no game.
func (s *Session) UpdateStatus(idle int, game string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, GameTypeGame, game, ""))
}

// UpdateStreamingStatus is used to update the user's streaming status.
// If idle>0 then set status to idle.
// If game!="" then set game.
// If game!="" and url!="" then set the status type to streaming with the URL set.
// if otherwise, set status to active, and no game.
func (s *Session) UpdateStreamingStatus(idle int, game string, url string) (err error) {
	gameType := GameTypeGame
	if url != "" {
		gameType = GameTypeStreaming
	}
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, gameType, game, url))
}

// UpdateListeningStatus is used to set the user to "Listening to..."
// If game!="" then set to what user is listening to
// Else, set user to active and no game.
func (s *Session) UpdateListeningStatus(game string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(0, GameTypeListening, game, ""))
}

// UpdateStatusComplex allows for sending the raw status update data untouched by discordgo.
func (s *Session) UpdateStatusComplex(usd UpdateStatusData) (err error) {

	s.RLock()
	defer s.RUnlock()
	if s.wsConn == nil {
		return ErrWSNotFound
	}

	s.wsMutex.Lock()
	err = s.wsConn.WriteJSON(updateStatusOp{3, usd})
	s.wsMutex.Unlock()

	return
}

type requestGuildMembersData struct {
	GuildID string `json:"guild_id"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
}

type requestGuildMembersOp struct {
	Op   int                     `json:"op"`
	Data requestGuildMembersData `json:"d"`
}

// RequestGuildMembers requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID  : The ID of the guild to request members of
// query    : String that username starts with, leave empty to return all members
// limit    : Max number of items to return, or 0 to request all members matched
func (s *Session) RequestGuildMembers(guildID, query string, limit int) (err error) {
	log.Infof("called")

	s.RLock()
	defer s.RUnlock()
	if s.wsConn == nil {
		return ErrWSNotFound
	}

	data := requestGuildMembersData{
		GuildID: guildID,
		Query:   query,
		Limit:   limit,
	}

	s.wsMutex.Lock()
	err = s.wsConn.WriteJSON(requestGuildMembersOp{8, data})
	s.wsMutex.Unlock()

	return
}

// onEvent is the "event handler" for all messages received on the
// Discord Gateway API websocket connection.
//
// If you use the AddHandler() function to register a handler for a
// specific event this function will pass the event along to that handler.
//
// If you use the AddHandler() function to register a handler for the
// "OnEvent" event then all events will be passed to that handler.
func (s *Session) onEvent(messageType int, message []byte) (*Event, error) {

	var err error
	var reader io.Reader
	reader = bytes.NewBuffer(message)

	// If this is a compressed message, uncompress it.
	if messageType == websocket.BinaryMessage {

		z, err2 := zlib.NewReader(reader)
		if err2 != nil {
			log.Errorf("error uncompressing websocket message, %s", err)
			return nil, err2
		}

		defer func() {
			err3 := z.Close()
			if err3 != nil {
				log.Warnf("error closing zlib, %s", err)
			}
		}()

		reader = z
	}

	// Decode the event into an Event struct.
	var e *Event
	decoder := json.NewDecoder(reader)
	if err = decoder.Decode(&e); err != nil {
		log.Errorf("error decoding websocket message, %s", err)
		return e, err
	}

	log.Debugf("Op: %d, Seq: %d, Type: %s, Data: %s\n\n", e.Opcode, e.Sequence, e.Type, string(e.RawData))

	// Ping request.
	// Must respond with a heartbeat packet within 5 seconds
	if e.Opcode == 1 { // TODO
		log.Infof("sending heartbeat in response to Op1")
		s.wsMutex.Lock()
		err = s.wsConn.WriteJSON(heartbeatOp{1, atomic.LoadInt64(s.sequence)})
		s.wsMutex.Unlock()
		if err != nil {
			log.Errorf("error sending heartbeat in response to Op1")
			return e, err
		}

		return e, nil
	}

	// Reconnect
	// Must immediately disconnect from gateway and reconnect to new gateway.
	if e.Opcode == 7 { // TODO
		log.Infof("Closing and reconnecting in response to Op7")
		s.Close()
		s.reconnect()
		return e, nil
	}

	// Invalid Session
	// Must respond with a Identify packet.
	if e.Opcode == 9 { // TODO

		log.Infof("sending identify packet to gateway in response to Op9")

		err = s.identify()
		if err != nil {
			log.Warnf("error sending gateway identify packet, %s, %s", s.gateway, err)
			return e, err
		}

		return e, nil
	}

	if e.Opcode == 10 { // TODO
		// Op10 is handled by Open()
		return e, nil
	}

	if e.Opcode == 11 { // TODO
		s.Lock()
		s.LastHeartbeatAck = time.Now().UTC()
		s.Unlock()
		log.Infof("got heartbeat ACK")
		return e, nil
	}

	// Do not try to Dispatch a non-Dispatch Message
	if e.Opcode != 0 { // TODO
		// But we probably should be doing something with them.
		// TEMP
		log.Warnf("unknown Op: %d, Seq: %d, Type: %s, Data: %s, message: %s", e.Opcode, e.Sequence, e.Type, string(e.RawData), string(message))
		return e, nil
	}

	// Store the message sequence
	atomic.StoreInt64(s.sequence, e.Sequence)

	// Map event to registered event handlers and pass it along to any registered handlers.
	if eh, ok := registeredInterfaceProviders[e.Type]; ok {
		e.Struct = eh.New()

		// Attempt to unmarshal our event.
		if err = json.Unmarshal(e.RawData, e.Struct); err != nil {
			log.Errorf("error unmarshalling %s event, %s", e.Type, err)
		}

		// Send event to any registered event handlers for it's type.
		// Because the above doesn't cancel this, in case of an error
		// the struct could be partially populated or at default values.
		// However, most errors are due to a single field and I feel
		// it's better to pass along what we received than nothing at all.
		// TODO: Think about that decision :)
		// Either way, READY events must fire, even with errors.
		s.handleEvent(e.Type, e.Struct)
	} else {
		log.Warnf("unknown event: Op: %d, Seq: %d, Type: %s, Data: %s", e.Opcode, e.Sequence, e.Type, string(e.RawData))
	}

	// For legacy reasons, we send the raw event also, this could be useful for handling unknown events.
	s.handleEvent(eventEventType, e)

	return e, nil
}

// ------------------------------------------------------------------------------------------------
// Code related to voice connections that initiate over the data websocket
// ------------------------------------------------------------------------------------------------

// ChannelVoiceJoin joins the session user to a voice channel.
//
//    gID     : Guild ID of the channel to join.
//    cID     : Channel ID of the channel to join.
//    mute    : If true, you will be set to muted upon joining.
//    deaf    : If true, you will be set to deafened upon joining.
func (s *Session) ChannelVoiceJoin(gID, cID string, mute, deaf bool) (v voice.Connection, err error) {

	log.Infof("called")

	s.RLock()
	v, _ = s.VoiceConnections[gID]
	s.RUnlock()

	if v == nil {
		v = voice.NewConnection(&s.wsMutex, &s.wsConn, s.VoiceConnections, s.(*sync.Mutex))
		s.Lock()
		s.VoiceConnections[gID] = v
		s.Unlock()
	}

	v.Update(gID, cID, deaf, mute)

	// Send the request to Discord that we want to join the voice channel
	data := voice.ChannelJoinClientOp4{4, voice.ChannelJoinDataClientOp4{&gID, &cID, mute, deaf}} // todo method
	s.wsMutex.Lock()
	err = s.wsConn.WriteJSON(data)
	s.wsMutex.Unlock()
	if err != nil {
		return
	}

	// doesn't exactly work perfect yet.. TODO
	err = v.waitUntilConnected()
	if err != nil {
		log.Warnf("error waiting for voice to connect, %s", err)
		v.Close()
		return
	}

	return
}

// onVoiceStateUpdate handles Voice State Update events on the data websocket.
func (s *Session) onVoiceStateUpdate(st *VoiceStateUpdate) {

	// If we don't have a connection for the channel, don't bother
	if st.ChannelID == "" {
		return
	}

	// Check if we have a voice connection to update
	s.RLock()
	v, exists := s.VoiceConnections[st.GuildID]
	s.RUnlock()
	if !exists {
		return
	}

	// We only care about events that are about us.
	if s.State.User.ID != st.UserID {
		return
	}

	// Store the SessionID for later use.
	v.Lock()
	v.UserID = st.UserID
	v.sessionID = st.SessionID
	v.ChannelID = st.ChannelID
	v.Unlock()
}

// onVoiceServerUpdate handles the Voice Server Update data websocket event.
//
// This is also fired if the Guild's voice region changes while connected
// to a voice channel.  In that case, need to re-establish connection to
// the new region endpoint.
func (s *Session) onVoiceServerUpdate(st *VoiceServerUpdate) {

	log.Debug("called")

	s.RLock()
	v, exists := s.VoiceConnections[st.GuildID]
	s.RUnlock()

	// If no VoiceConnection exists, just skip this
	if !exists {
		return
	}

	// If currently connected to voice ws/udp, then disconnect.
	// Has no effect if not connected.
	v.Close()

	// Store values for later use
	v.Lock()
	v.token = st.Token
	v.endpoint = st.Endpoint
	v.GuildID = st.GuildID
	v.Unlock()

	// Open a connection to the voice server
	err := v.Open()
	if err != nil {
		log.Errorf("onVoiceServerUpdate voice.open, %s", err)
	}
}

type identifyProperties struct {
	OS              string `json:"$os"`
	Browser         string `json:"$browser"`
	Device          string `json:"$device"`
	Referer         string `json:"$referer"`
	ReferringDomain string `json:"$referring_domain"`
}

type identifyData struct {
	Token          string             `json:"token"`
	Properties     identifyProperties `json:"properties"`
	LargeThreshold int                `json:"large_threshold"`
	Compress       bool               `json:"compress"`
	Shard          *[2]int            `json:"shard,omitempty"`
}

type identifyOp struct {
	Op   int          `json:"op"`
	Data identifyData `json:"d"`
}

// identify sends the identify packet to the gateway
func (s *Session) identify() error {

	properties := identifyProperties{runtime.GOOS,
		"Discordgo v" + VERSION,
		"",
		"",
		"",
	}

	data := identifyData{s.Token,
		properties,
		250,
		s.Compress,
		nil,
	}

	if s.ShardCount > 1 {

		if s.ShardID >= s.ShardCount {
			return ErrWSShardBounds
		}

		data.Shard = &[2]int{s.ShardID, s.ShardCount}
	}

	op := identifyOp{2, data}

	s.wsMutex.Lock()
	err := s.wsConn.WriteJSON(op)
	s.wsMutex.Unlock()

	return err
}

func (s *Session) reconnect() {

	log.Infof("called")

	var err error

	if s.ShouldReconnectOnError {

		wait := time.Duration(1)

		for {
			log.Infof("trying to reconnect to gateway")

			err = s.Open()
			if err == nil {
				log.Infof("successfully reconnected to gateway")

				// I'm not sure if this is actually needed.
				// if the gw reconnect works properly, voice should stay alive
				// However, there seems to be cases where something "weird"
				// happens.  So we're doing this for now just to improve
				// stability in those edge cases.
				s.RLock()
				defer s.RUnlock()
				for _, vc := range s.VoiceConnections {

					log.Infof("reconnecting voice connection to guild %s", vc.GuildID)
					go vc.Reconnect()

					// This is here just to prevent violently spamming the
					// voice reconnects
					time.Sleep(1 * time.Second)

				}
				return
			}

			// Certain race conditions can call reconnect() twice. If this happens, we
			// just break out of the reconnect loop
			if err == ErrWSAlreadyOpen {
				log.Infof("Websocket already exists, no need to reconnect")
				return
			}

			log.Errorf("error reconnecting to gateway, %s", err)

			<-time.After(wait * time.Second)
			wait *= 2
			if wait > 600 {
				wait = 600
			}
		}
	}
}

// Close closes a websocket and stops all listening/heartbeat goroutines.
// TODO: Add support for Voice WS/UDP connections
func (s *Session) Close() (err error) {

	log.Infof("called")
	s.Lock()

	s.DataReady = false

	if s.listening != nil {
		log.Infof("closing listening channel")
		close(s.listening)
		s.listening = nil
	}

	// TODO: Close all active Voice Connections too
	// this should force stop any reconnecting voice channels too

	if s.wsConn != nil {

		log.Infof("sending close frame")
		// To cleanly close a connection, a client should send a close
		// frame and wait for the server to close the connection.
		s.wsMutex.Lock()
		err := s.wsConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.wsMutex.Unlock()
		if err != nil {
			log.Infof("error closing websocket, %s", err)
		}

		// TODO: Wait for Discord to actually close the connection.
		time.Sleep(1 * time.Second)

		log.Infof("closing gateway websocket")
		err = s.wsConn.Close()
		if err != nil {
			log.Infof("error closing websocket, %s", err)
		}

		s.wsConn = nil
	}

	s.Unlock()

	log.Infof("emit disconnect event")
	s.handleEvent(disconnectEventType, &Disconnect{})

	return
}
