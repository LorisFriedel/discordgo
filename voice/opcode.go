package voice

type Opcode int

const (
	// Name					  Code	  Sent by		Description
	Identify           Opcode = 0  // client		begin a voice websocket connection
	SelectProtocol     Opcode = 1  // client		select the voice protocol
	Ready              Opcode = 2  // server		complete the websocket handshake
	Heartbeat          Opcode = 3  // client		keep the websocket connection alive
	SessionDescription Opcode = 4  // server		describe the session
	Speaking           Opcode = 5  // client/server	indicate which users are speaking
	HeartbeatACK       Opcode = 6  // server		sent immediately following a received client heartbeat
	Resume             Opcode = 7  // client		resume a connection
	Hello              Opcode = 8  // server		the continuous interval in milliseconds after which the client should send a heartbeat
	Resumed            Opcode = 9  // server		acknowledge Resume
	ClientDisconnect   Opcode = 13 // server		a client has disconnected from the voice channel
)
