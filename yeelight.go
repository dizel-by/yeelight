package yeelight

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	discoverMSG = "M-SEARCH * HTTP/1.1\r\n HOST:239.255.255.250:1982\r\n MAN:\"ssdp:discover\"\r\n ST:wifi_bulb\r\n"

	// timeout value for TCP and UDP commands
	timeout = time.Second * 3

	//SSDP discover address
	ssdpAddr = "239.255.255.250:1982"

	//CR-LF delimiter
	crlf = "\r\n"
)

type (
	//Command represents COMMAND request to Yeelight device
	Command struct {
		ID     int           `json:"id"`
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}

	// CommandResult represents response from Yeelight device
	CommandResult struct {
		ID     int           `json:"id"`
		Result []interface{} `json:"result,omitempty"`
		Error  *Error        `json:"error,omitempty"`
	}

	// Notification represents notification response
	Notification struct {
		Method string                 `json:"method"`
		Params map[string]interface{} `json:"params"`
	}

	//Error struct represents error part of response
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	State struct {
		Address    string
		Name       string
		Power      string
		Brightness string
	}

	//Yeelight represents device
	Yeelight struct {
		addr  string
		rnd   *rand.Rand
		state State
	}
)

var id int = 0

//Discover discovers device in local network via ssdp
func Discover() (*Yeelight, error) {
	var err error

	ssdp, _ := net.ResolveUDPAddr("udp4", ssdpAddr)
	c, _ := net.ListenPacket("udp4", getIP()+":0")
	socket := c.(*net.UDPConn)
	socket.WriteToUDP([]byte(discoverMSG), ssdp)
	socket.SetReadDeadline(time.Now().Add(timeout))

	rsBuf := make([]byte, 1024)
	size, _, err := socket.ReadFromUDP(rsBuf)
	if err != nil {
		return nil, errors.New("no devices found")
	}
	rs := rsBuf[0:size]
	state, err := parseState(string(rs))
	return New(*state), nil

}

//New creates new device instance for address provided
func New(state State) *Yeelight {
	return &Yeelight{
		addr:  state.Address,
		state: state,
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}

}

func (y *Yeelight) GetState() State {
	return y.state
}

// Listen connects to device and listens for NOTIFICATION events
func (y *Yeelight) Listen() (<-chan *Notification, error) {
	var err error
	notifCh := make(chan *Notification)

	conn, err := net.DialTimeout("tcp", y.addr, time.Second*3)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to %s. %s", y.addr, err)
	}

	//fmt.Println("Connection established")
	go func(c net.Conn) {
		//make sure connection is closed when method returns
		defer closeConnection(conn)

		connReader := bufio.NewReader(c)
		for {
			data, err := connReader.ReadString('\n')
			if nil == err {
				var rs Notification
				//fmt.Println(data)
				json.Unmarshal([]byte(data), &rs)
				//fmt.Printf("%+v\n", rs.Params)
				select {
				case notifCh <- &rs:
				default:
					fmt.Println("Channel is full")
				}
			}
		}
	}(conn)

	return notifCh, nil
}

// GetProp method is used to retrieve current property of smart LED.
func (y *Yeelight) GetProp(values ...interface{}) ([]interface{}, error) {
	r, err := y.executeCommand("get_prop", values)
	if nil != err {
		return nil, err
	}
	return r.Result, nil
}

//SetPower is used to switch on or off the smart LED (software managed on/off).
func (y *Yeelight) SetPower(on string) error {
	_, err := y.executeCommand("set_power", []interface{}{on, "sudden", 0})
	return err
}

func (y *Yeelight) SetBright(bright string) error {
	br, err := strconv.Atoi(bright)
	if err != nil {
		return err
	}
	_, err = y.executeCommand("set_bright", []interface{}{br, "sudden", 0})
	return err
}

func (y *Yeelight) randID() int {
	i := y.rnd.Intn(100)
	return i
}

func (y *Yeelight) newCommand(name string, params []interface{}) *Command {
	id = id + 1
	return &Command{
		Method: name,
		ID:     id,
		Params: params,
	}
}

//executeCommand executes command with provided parameters
func (y *Yeelight) executeCommand(name string, params []interface{}) (*CommandResult, error) {
	return y.execute(y.newCommand(name, params))
}

//executeCommand executes command
func (y *Yeelight) execute(cmd *Command) (*CommandResult, error) {

	conn, err := net.Dial("tcp", y.addr)
	if nil != err {
		return nil, fmt.Errorf("cannot open connection to %s. %s", y.addr, err)
	}
	defer conn.Close()

	//time.Sleep(time.Second)
	conn.SetReadDeadline(time.Now().Add(timeout))

	//write request/command
	b, _ := json.Marshal(cmd)
	//fmt.Println(string(b))
	fmt.Fprint(conn, string(b)+crlf)

	//wait and read for response
	res, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("cannot read command result %s", err)
	}
	var rs CommandResult
	err = json.Unmarshal([]byte(res), &rs)
	if nil != err {
		return nil, fmt.Errorf("cannot parse command result %s", err)
	}
	if nil != rs.Error {
		return nil, fmt.Errorf("command execution error. Code: %d, Message: %s", rs.Error.Code, rs.Error.Message)
	}
	return &rs, nil
}

func parseState(msg string) (*State, error) {
	if strings.HasSuffix(msg, crlf) {
		msg = msg + crlf
	}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(msg)), nil)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	defer resp.Body.Close()

	return &State{
		Address:    strings.TrimPrefix(resp.Header.Get("LOCATION"), "yeelight://"),
		Name:       resp.Header.Get("NAME"),
		Power:      resp.Header.Get("POWER"),
		Brightness: resp.Header.Get("BRIGHT"),
	}, nil
}

//closeConnection closes network connection
func closeConnection(c net.Conn) {
	if nil != c {
		c.Close()
	}
}
