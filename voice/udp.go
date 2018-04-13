package voice

import (
	"time"
	"fmt"
	"strings"
	"net"
	"encoding/binary"

	"golang.org/x/crypto/nacl/secretbox"

	log "github.com/sirupsen/logrus"
)

type udpInterface interface {
	Reconnect()

	udpOpen() (err error)
	udpKeepAlive(udpConn *net.UDPConn, close <-chan struct{}, period time.Duration)
	opusSender(udpConn *net.UDPConn, close <-chan struct{}, write <-chan []byte, rate, size int)
	opusReceiver(udpConn *net.UDPConn, close <-chan struct{}, read chan<- *Packet)
}

// Opcode 1 - CLIENT
type SelectProtocolClientOp1 struct {
	Op   int                         `json:"op"` // Always 1
	Data SelectProtocolDataClientOp1 `json:"d"`
}

type SelectProtocolDataClientOp1 struct {
	Protocol string           `json:"protocol"` // Always "udp" ?
	Data     UDPDataClientOp1 `json:"data"`
}

type UDPDataClientOp1 struct {
	Address string `json:"address"` // Public IP of machine running this code
	Port    uint16 `json:"port"`    // UDP Port of machine running this code
	Mode    string `json:"mode"`    // always "xsalsa20_poly1305"
}

// A Packet contains the headers and content of a received voice packet.
type Packet struct {
	SSRC      uint32
	Sequence  uint16
	Timestamp uint32
	Type      []byte
	Opus      []byte
	PCM       []int16
}

// udpOpen opens a UDP connection to the voice server and completes the
// initial required handshake.  This connection is left open in the session
// and can be used to send or receive audio.  This should only be called
// from voice.wsEvent OP2
func (c *ConnectionImpl) udpOpen() (err error) {

	c.Lock()
	defer c.Unlock()

	if c.wsConn == nil {
		return fmt.Errorf("nil voice websocket")
	}

	if c.udpConn != nil {
		return fmt.Errorf("udp connection already open")
	}

	if c.close == nil {
		return fmt.Errorf("nil close channel")
	}

	if c.endpoint == "" {
		return fmt.Errorf("empty endpoint")
	}

	log.Debug(c.endpoint)
	host := fmt.Sprintf("%s:%d", strings.TrimSuffix(c.endpoint, ":80"), c.op2.Port)
	log.Debug(host)
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		log.Errorf("error resolving udp host %s, %s", host, err)
		return
	}

	log.Debugf("connecting to udp addr %s", addr.String())
	c.udpConn, err = net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Errorf("error connecting to udp addr %s, %s", addr.String(), err)
		return
	}

	// Create a 70 byte array and put the SSRC code from the Op 2 VoiceConnection event
	// into it.  Then send that over the UDP connection to Discord
	sb := make([]byte, 70)
	binary.BigEndian.PutUint32(sb, c.op2.SSRC)
	_, err = c.udpConn.Write(sb)
	if err != nil {
		log.Errorf("udp write error to %s, %s", addr.String(), err)
		return
	}

	// Create a 70 byte array and listen for the initial handshake response
	// from Discord.  Once we get it parse the IP and PORT information out
	// of the response.  This should be our public IP and PORT as Discord
	// saw us.
	rb := make([]byte, 70)
	rlen, _, err := c.udpConn.ReadFromUDP(rb)
	if err != nil {
		log.Errorf("udp read error, %s, %s", addr.String(), err)
		return
	}

	if rlen < 70 {
		log.Warn("received udp packet too small")
		return fmt.Errorf("received udp packet too small")
	}

	// Loop over position 4 through 20 to grab the IP address
	// Should never be beyond position 20.
	var ip string
	for i := 4; i < 20; i++ {
		if rb[i] == 0 {
			break
		}
		ip += string(rb[i])
	}

	// Grab port from position 68 and 69
	port := binary.LittleEndian.Uint16(rb[68:70])

	// Take the data from above and send it back to Discord to finalize
	// the UDP connection handshake.
	data := SelectProtocolClientOp1{1, SelectProtocolDataClientOp1{"udp", UDPDataClientOp1{ip, port, "xsalsa20_poly1305"}}}

	c.wsMutex.Lock()
	err = c.wsConn.WriteJSON(data)
	c.wsMutex.Unlock()
	if err != nil {
		log.Errorf("udp write error, %#v, %s", data, err)
		return
	}

	// start udpKeepAlive
	go c.udpKeepAlive(c.udpConn, c.close, 4*time.Second)
	// TODO: find a way to check that it fired off okay

	log.Debugf("successfully connected to udp addr %s", addr.String())

	return
}

// udpKeepAlive sends a udp packet to keep the udp connection open
// This is still a bit of a "proof of concept"
func (c *ConnectionImpl) udpKeepAlive(udpConn *net.UDPConn, close <-chan struct{}, period time.Duration) {

	if udpConn == nil || close == nil {
		return
	}

	var err error
	var sequence uint64

	packet := make([]byte, 8)

	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		binary.LittleEndian.PutUint64(packet, sequence)
		sequence++

		_, err = udpConn.Write(packet)
		if err != nil {
			log.Errorf("write error, %s", err)
			return
		}

		select {
		case <-ticker.C:
			// continue loop and send keepalive
		case <-close:
			return
		}
	}
}

// opusSender will listen on the given channel and send any
// pre-encoded opus audio to Discord.  Supposedly.
func (c *ConnectionImpl) opusSender(udpConn *net.UDPConn, close <-chan struct{}, write <-chan []byte, rate, size int) {

	if udpConn == nil || close == nil {
		return
	}

	// VoiceConnection is now ready to receive audio packets
	// TODO: this needs reviewed as I think there must be a better way.
	c.Lock()
	c.Ready = true
	c.Unlock()
	defer func() {
		c.Lock()
		c.Ready = false
		c.Unlock()
	}()

	var sequence uint16
	var timestamp uint32
	var recvbuf []byte
	var ok bool
	udpHeader := make([]byte, 12)
	var nonce [24]byte

	// build the parts that don't change in the udpHeader
	udpHeader[0] = 0x80
	udpHeader[1] = 0x78
	binary.BigEndian.PutUint32(udpHeader[8:], c.op2.SSRC)

	// start a send loop that loops until buf chan is closed
	ticker := time.NewTicker(time.Millisecond * time.Duration(size/(rate/1000)))
	defer ticker.Stop()
	for {

		// Get data from chan.  If chan is closed, return.
		select {
		case <-close:
			return
		case recvbuf, ok = <-write:
			if !ok {
				return
			}
			// else, continue loop
		}

		c.RLock()
		speaking := c.speaking
		c.RUnlock()
		if !speaking {
			err := c.Speaking(true)
			if err != nil {
				log.Errorf("error sending speaking packet, %s", err)
			}
		}

		// Add sequence and timestamp to udpPacket
		binary.BigEndian.PutUint16(udpHeader[2:], sequence)
		binary.BigEndian.PutUint32(udpHeader[4:], timestamp)

		// encrypt the opus data
		copy(nonce[:], udpHeader)
		c.RLock()
		sendbuf := secretbox.Seal(udpHeader, recvbuf, &nonce, &c.op4.SecretKey)
		c.RUnlock()

		// block here until we're exactly at the right time :)
		// Then send rtp audio packet to Discord over UDP
		select {
		case <-close:
			return
		case <-ticker.C:
			// continue
		}
		_, err := udpConn.Write(sendbuf)

		if err != nil {
			log.Errorf("udp write error, %s", err)
			log.Debugf("voice struct: %#v\n", c)
			return
		}

		if (sequence) == 0xFFFF {
			sequence = 0
		} else {
			sequence++
		}

		if (timestamp + uint32(size)) >= 0xFFFFFFFF {
			timestamp = 0
		} else {
			timestamp += uint32(size)
		}
	}
}

// opusReceiver listens on the UDP socket for incoming packets
// and sends them across the given channel
// NOTE :: This function may change names later.
func (c *ConnectionImpl) opusReceiver(udpConn *net.UDPConn, close <-chan struct{}, read chan<- *Packet) {

	if udpConn == nil || close == nil {
		return
	}

	recvbuf := make([]byte, 4096)
	var nonce [24]byte

	log.Debug("called")

	for {
		rlen, err := udpConn.Read(recvbuf)

		if err != nil {
			// Detect if we have been closed manually. If a Close() has already
			// happened, the udp connection we are listening on will be different
			// to the current session.
			c.RLock()
			sameConnection := c.udpConn == udpConn
			c.RUnlock()
			if sameConnection {

				log.Errorf("udp read error, %s, %s", c.endpoint, err)
				log.Debugf("voice struct: %#v\n", c)

				go c.reconnect()
			}
			return
		}

		select {
		case <-close:
			return
		default:
			// continue loop
		}

		// For now, skip anything except audio.
		if rlen < 12 || (recvbuf[0] != 0x80 && recvbuf[0] != 0x90) {
			continue
		}

		// build a audio packet struct
		p := Packet{}
		p.Type = recvbuf[0:2]
		p.Sequence = binary.BigEndian.Uint16(recvbuf[2:4])
		p.Timestamp = binary.BigEndian.Uint32(recvbuf[4:8])
		p.SSRC = binary.BigEndian.Uint32(recvbuf[8:12])
		// decrypt opus data
		copy(nonce[:], recvbuf[0:12])
		p.Opus, _ = secretbox.Open(nil, recvbuf[12:rlen], &nonce, &c.op4.SecretKey)

		if len(p.Opus) > 8 && recvbuf[0] == 0x90 {
			// Extension bit is set, first 8 bytes is the extended header
			p.Opus = p.Opus[8:]
		}

		if c != nil {
			select {
			case read <- &p:
			case <-close:
				return
			}
		}
	}
}

// Reconnect will close down a voice connection then immediately try to
// reconnect to that session.
// NOTE : This func is messy and a WIP while I find what works.
// It will be cleaned up once a proven stable option is flushed out.
// aka: this is ugly shit code, please don't judge too harshly.
func (c *ConnectionImpl) Reconnect() {
	log.Debug("not properly implemented yet, won't reconnect")

	//log.Debug("called")
	//
	//v.Lock()
	//if v.reconnecting {
	//	log.Infof("already reconnecting to channel %s, exiting", v.ChannelID)
	//	v.Unlock()
	//	return
	//}
	//v.reconnecting = true
	//v.Unlock()
	//
	//defer func() { v.reconnecting = false }()
	//
	//// Close any currently open connections
	//v.Close()
	//
	//wait := time.Duration(1)
	//for {
	//
	//	<-time.After(wait * time.Second)
	//	wait *= 2
	//	if wait > 600 {
	//		wait = 600
	//	}
	//	// TODO
	//	if v.session.DataReady == false || v.sWsConn == nil {
	//		log.Warnf("cannot reconnect to channel %s with unready session", v.ChannelID)
	//		continue
	//	}
	//
	//	log.Infof("trying to reconnect to channel %s", v.ChannelID)
	//
	//	_, err := v.session.ChannelVoiceJoin(v.GuildID, v.ChannelID, v.mute, v.deaf)
	//	if err == nil {
	//		log.Infof("successfully reconnected to channel %s", v.ChannelID)
	//		return
	//	}
	//
	//	log.Infof("error reconnecting to channel %s, %s", v.ChannelID, err)
	//
	//	// if the reconnect above didn't work lets just send a disconnect
	//	// packet to reset things.
	//	// Send a OP4 with a nil channel to disconnect
	//	data := ChannelJoinClientOp4{4, ChannelJoinDataClientOp4{&v.GuildID, nil, true, true}}
	//	v.sWsMutex.Lock()
	//	err = v.sWsConn.WriteJSON(data)
	//	v.sWsMutex.Unlock()
	//	if err != nil {
	//		log.Errorf("error sending disconnect packet, %s", err)
	//	}
	//}
}
