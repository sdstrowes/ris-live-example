package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

var done chan interface{}
var interrupt chan os.Signal

//{
//	"type":"ris_message",
//	"data": {
//		"timestamp":1695269583.730,
//		"peer":"217.29.66.158",
//		"peer_asn":"24482",
//		"id":"217.29.66.158-018ab5f0fb720005",
//		"host":"rrc10.ripe.net",
//		"type":"UPDATE",
//		"path":[24482,6939,38040,23969],
//		"community":[[24482,2],[24482,200],[24482,12000],[24482,12040],[24482,12041],[24482,20100]],
//		"origin":"IGP",
//		"med":0,
//		"announcements":[{"next_hop":"217.29.66.158","prefixes":["1.1.249.0/24"]}],
//		"withdrawals":[]
//	}
//}

type RisCommunity struct {
	parts []int
}

type RisAnnouncement struct {
	NextHop  string   `json:next_hop`
	Prefixes []string `json:prefixes`
}

type RisWithdrawal struct {
}

type RisLiveMessageData struct {
	Timestamp     float64           `json:timestamp`
	Peer          string            `json:peer`
	PeerAsn       string            `json:"peer_asn"` //,"peer_asn":"396998"
	Id            string            `json:id`
	Host          string            `json:host`
	Type          string            `json:type`
	Path          []int             `json:path`
	Community     [][]int           `json:community`
	Origin        string            `json:origin`
	Med           int               `json:med`
	Announcements []RisAnnouncement `json:announcements`
	Withdrawals   []RisWithdrawal   `json:withdrawals`
}

type RisLiveMessage struct {
	Type string             `json:"type"`
	Data RisLiveMessageData `json:"data"`
}

// This does almost nothing; simply spits out the message just received (including responses to keepalive messages)
func receiveHandler(connection *websocket.Conn) {
	defer close(done)
	for {
		_, msg, err := connection.ReadMessage()
		if err != nil {
			log.Println("Error in receive:", err)
			return
		}

		var message RisLiveMessage
		err = json.Unmarshal(msg, &message)
		if err != nil {
			log.Println("Bad parse:", err)
			log.Println("Original message:", msg)
		}

		if message.Type != "ris_message" {
			log.Println("Received unhandled message:", message)
		} else {
			payload := message.Data
			if payload.Type == "UPDATE" {
				prefixes := fmt.Sprintf("%s", payload.Announcements)
				// truncate; 48 is totally arbitrary here
				if len(prefixes) > 48 {
					prefixes = prefixes[:44] + "...]"
				}
				path := strings.Trim(strings.Join(strings.Split(fmt.Sprint(payload.Path), " "), " "), "[]")
				log.Printf("%s %s collector:%s, neighbor:%s, prefixes:%s, aspath:%s\n", strconv.FormatFloat(payload.Timestamp, 'f', 3, 64), payload.Type, payload.Host, payload.PeerAsn, prefixes, path)
			} else {
				fmt.Println("")
				fmt.Printf("%s\n", msg)
				log.Println(strconv.FormatFloat(payload.Timestamp, 'f', 3, 64), payload.Type, payload.Host, payload.PeerAsn, payload.Withdrawals)
			}
		}
	}
}

type RisMessageData struct {
	Host   string `json:"host,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

type RisMessage struct {
	Type string          `json:"type"`
	Data *RisMessageData `json:"data"`
}

func main() {
	done = make(chan interface{})    // Channel to indicate that the receiverHandler is done
	interrupt = make(chan os.Signal) // Channel to listen for interrupt signal to terminate gracefully

	signal.Notify(interrupt, os.Interrupt) // Notify the interrupt channel for SIGINT

	socketUrl := "ws://ris-live.ripe.net/v1/ws/"
	conn, _, err := websocket.DefaultDialer.Dial(socketUrl, nil)
	if err != nil {
		log.Fatal("Error connecting to Websocket Server:", err)
	}
	defer conn.Close()
	go receiveHandler(conn)

	/* Ping message (re-send this every minute or so */
	ping := RisMessage{"ping", nil}
	pingstr, err := json.Marshal(ping)
	if err != nil {
		log.Fatal("Error marshalling ping message (!)")
		return
	}

	/* Subscribe */
	subscription1 := RisMessage{"ris_subscribe", &RisMessageData{"", "0.0.0.0/0"}}

	// alternatives:
	// this would listen to one of Fastly's blocks of address space, from all collectors:
	//subscription1 := RisMessage{"ris_subscribe", &RisMessageData{"", "151.101.0.0/16"}}
	// this would listen to all of the IPv4 address space, but from only one collector:
	//subscription1 := RisMessage{"ris_subscribe", &RisMessageData{"rrc21", "0.0.0.0/0"}}

	out1, err := json.Marshal(subscription1)
	if err != nil {
		log.Fatal("Error marshalling subscription message (!)")
		return
	}
	log.Println("Subscribing to: ", subscription1)
	conn.WriteMessage(websocket.TextMessage, out1)

	// You can do more than one subscription
	//subscription2 := RisMessage{"ris_subscribe", &RisMessageData{"", "2a04:4e42::/48"}}
	//log.Println("Subscribing to: ", subscription2)
	//out2, err := json.Marshal(subscription2)
	//if err != nil {
	//	log.Fatal("Error marshalling subscription message (!)")
	//}
	//conn.WriteMessage(websocket.TextMessage, out2)

	for {
		select {
		case <-time.After(time.Duration(60) * time.Millisecond * 1000):
			// Send an echo packet 60 seconds
			err := conn.WriteMessage(websocket.TextMessage, pingstr)
			if err != nil {
				log.Println("Error during writing to websocket:", err)
				return
			}

		case <-interrupt:
			// We received a SIGINT; clean up
			log.Println("Received SIGINT interrupt signal. Closing all pending connections")
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("Error during closing websocket: ", err)
				return
			}

			select {
			case <-done:
				log.Println("Receiver channel closed, exiting")
			case <-time.After(time.Duration(1) * time.Second):
				log.Println("Timeout in closing receiving channel; exiting")
			}
			return
		}
	}
}
