package mpdfav

import (
	"errors"
	"fmt"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"log"
	"sync"
)

const (
	network         = "tcp"
	infoFieldSep    = ": "
	StickerSongType = "song"
)

type ChannelMessage struct {
	Channel string
	Message string
}

type Info map[string]string

var stickerGetRegexp = regexp.MustCompile("sticker: (.+)=(.*)")
var channelMessageRegexp = regexp.MustCompile("channel: (.+)\nmessage: (.+)")

func (i *Info) Progress() (int, int) {
	if t, ok := (*i)["time"]; ok {
		fieldSepIndex := strings.Index(t, ":")
		current, err := strconv.ParseFloat(t[0:fieldSepIndex], 0)
		total, err := strconv.ParseFloat(t[fieldSepIndex+1:], 0)
		if err != nil {
			return 0, 0
		}
		return int(current), int(total)
	}
	return 0, 0
}

func (info *Info) AddInfo(data string) error {
	fieldSepIndex := strings.Index(data, infoFieldSep)
	if fieldSepIndex == -1 {
		return errors.New(fmt.Sprintf("Invalid input: %s", data))
	}
	key := data[0:fieldSepIndex]
	(*info)[key] = data[fieldSepIndex+len(infoFieldSep):]
	return nil
}

type MPDClient struct {
	Host string
	Port uint
	conn *textproto.Conn
	quitCh chan bool
	c *sync.Cond
	subscriptions []*idleSubscription
}

func isMPDError(line string) bool {
	return strings.Index(line, "ACK") != -1
}

func fillInfoUntilOK(c *MPDClient, info *Info) error {
	for {
		line, err := c.conn.ReadLine()
		if err != nil {
			return err
		}
		if line == "OK" {
			break
		}
		err = info.AddInfo(line)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *MPDClient) CurrentSong() (Info, error) {
	id, err := c.Cmd("currentsong")
	if err != nil {
		return nil, err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	info := make(Info)
	err = fillInfoUntilOK(c, &info)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (c *MPDClient) Status() (Info, error) {
	id, err := c.Cmd("status")
	if err != nil {
		return nil, err
	}
	c.startResponse(id)
	defer c.endResponse(id)
	info := make(Info)
	err = fillInfoUntilOK(c, &info)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (c *MPDClient) StickerGet(stype, uri, stickerName string) (string, error) {
	id, err := c.Cmd(fmt.Sprintf(
		"sticker get \"%s\" \"%s\" \"%s\"",
		stype,
		uri,
		stickerName,
	))

	if err != nil {
		return "", err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	line, err := c.conn.ReadLine()
	match := stickerGetRegexp.FindStringSubmatch(line)
	if match == nil {
		if !isMPDError(line) {
			return "", nil
		}
		// If we found the song but no sticker, return empty string
		if strings.Index(line, "no such sticker") != -1 {
			return "", nil
		}
		return "", errors.New("StickerGet: " + line)
	}
	value := match[2]
	// OK line appears if it's ok,
	// otherwise only one line with error
	okLine, err := c.conn.ReadLine()
	if err != nil {
		return "", err
	}
	if okLine != "OK" {
		return "", errors.New("StickerGet didn't receive OK line: " + okLine)
	}
	return value, nil
}

func (c *MPDClient) StickerSet(stype, uri, stickerName, value string) error {
	id, err := c.Cmd(fmt.Sprintf(
		"sticker set \"%s\" \"%s\" \"%s\" \"%s\"",
		stype,
		uri,
		stickerName,
		value,
	))
	if err != nil {
		return err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	line, err := c.conn.ReadLine()
	if line != "OK" {
		return errors.New("StickerSet: " + line)
	}

	return nil
}

func (c *MPDClient) Subscribe(channel string) error {
	id, err := c.Cmd(fmt.Sprintf(
		"subscribe \"%s\"",
		channel,
	))
	if err != nil {
		return err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	line, err := c.conn.ReadLine()
	if line != "OK" {
		return errors.New("Subscribe: " + line)
	}

	return nil
}

func (c *MPDClient) Unsubscribe(channel string) error {
	id, err := c.Cmd(fmt.Sprintf(
		"unsubscribe \"%s\"",
		channel,
	))
	if err != nil {
		return err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	line, err := c.conn.ReadLine()
	if line != "OK" {
		return errors.New("Unsubscribe: " + line)
	}

	return nil
}

func (c *MPDClient) ReadMessages() ([]ChannelMessage, error) {
	id, err := c.Cmd("readmessages")
	if err != nil {
		return nil, err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	msgs := make([]ChannelMessage, 0)
	for {
		channelOrOkline, err := c.conn.ReadLine()
		if err != nil {
			return msgs, err
		}
		if channelOrOkline == "OK" {
			return msgs, nil
		}
		messageLine, err := c.conn.ReadLine()
		match := channelMessageRegexp.FindStringSubmatch(fmt.Sprintf(`%s
%s`, channelOrOkline, messageLine))
		if match == nil {
			return nil, errors.New(fmt.Sprintf("ReadMessages: bad channel/message response: %s,%s", channelOrOkline, messageLine))
		}
		msgs = append(msgs, ChannelMessage{match[1], match[2]})
	}
}

func (c *MPDClient) SendMessage(channel, text string) error {
	id, err := c.Cmd(fmt.Sprintf(
		"sendmessage \"%s\" \"%s\"",
		channel,
		text,
	))

	if err != nil {
		return err
	}
	c.startResponse(id)
	defer c.endResponse(id)

	line, err := c.conn.ReadLine()
	if line != "OK" {
		return errors.New("SendMessage: " + line)
	}

	return nil
}

type idleSubscription struct {
	ch chan string
	active bool
	subsystems []string
}

func (is *idleSubscription) Close() {
	close(is.ch)
	is.active = false
}

func (c *MPDClient) Idle(subsystems ...string) chan string {
	is := idleSubscription{make(chan string), true, subsystems}
	c.subscriptions = append(c.subscriptions, &is)
	return is.ch
}

func (c *MPDClient) idle() {
	defer func() {
        if err := recover(); err != nil {
            log.Println("Panic in Idle mode:", err)
        }
    }()
	initialized := false
	for {
		log.Println("Entering idle mode")

		if initialized {
			select {
				case <-c.quitCh:
					return
				default:
					c.c.L.Lock()
					c.c.Wait()
					c.c.L.Unlock()
			}
		} else {
			initialized = true
		}
		id, err := c.conn.Cmd("idle")
		if err != nil {
			panic(err)
		}

		c.conn.StartResponse(id)

		log.Println("Idle mode ready")
		info := make(Info)
		err = fillInfoUntilOK(c, &info)
		c.conn.EndResponse(id)
		if err != nil {
			panic(err)
		}

		subsystem, ok := info["changed"]
		if ok {
			fmt.Println("Subsystem changed:", subsystem)
			for i, subscription := range c.subscriptions {
				if subscription.active == true {
					if len(subscription.subsystems) == 0 {
						subscription.ch <- subsystem
						subscription.Close()
					} else {
						for _, wantedSubsystem := range subscription.subsystems {
							if wantedSubsystem == subsystem {
								fmt.Println("sending", subsystem, "to", i)
								subscription.ch <- subsystem
								subscription.Close()
							}
						}
					}
				}
			}
		} else {
			fmt.Println("Noidle")
		}
	}
}

func (c *MPDClient) Cmd(cmd string) (uint, error) {
	err := c.noIdle()
	if err != nil {
		return 0, err
	}
	fmt.Println("cmd:", cmd)
	id, err := c.conn.Cmd(cmd)
	if err != nil {
		return id, err
	}
	return id, nil
}

func (c *MPDClient) startResponse(id uint) {
	c.conn.StartResponse(id)
}

func (c *MPDClient) endResponse(id uint) {
	c.conn.EndResponse(id)
}

func (c *MPDClient) noIdle() error {
	id, err := c.conn.Cmd("noidle")
	if err != nil {
		return err
	}
	c.conn.StartResponse(id)
	defer c.conn.EndResponse(id)

	return nil
}

func (c *MPDClient) Close() error {
	if c.conn != nil {
		// Shut down idle mode
		fmt.Println("sending quit command")
		go func() {
			c.quitCh<-true
			fmt.Println("sent quit command")
		}()
		err := c.noIdle()
		if err != nil {
			return err
		}

		// Close connection properly
		id, err := c.conn.Cmd("close")
		c.conn.StartResponse(id)
		c.conn.EndResponse(id)
		c.conn.Close()
		err = c.conn.Close()
		if err != nil {
			return err
		}
		c.conn = nil
	}
	return nil
}

func Connect(host string, port uint) (*MPDClient, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := textproto.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	line, err := conn.ReadLine()
	if err != nil {
		return nil, err
	}

	if line[0:6] != "OK MPD" {
		return nil, errors.New("MPD: not OK")
	}

	var m sync.Mutex
    c := sync.NewCond(&m)
	mpdc := &MPDClient{host, port, conn, make(chan bool), c, []*idleSubscription{}}
	//go mpdc.idle()
	return mpdc, nil
}
