// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go-websockets 

import (
	"bytes"
	"encoding/json"
	"github.com/sirupsen/logrus"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	id   int
}

type RawMessage struct {
	Type string
	Args json.RawMessage
}
type Message struct {
	Type string `json:"type"`
	Args interface{} `json:"args"`
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c.id
		c.conn.Close()
	}()
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		logrus.Tracef("start for loop for client %d", c.id)
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			logrus.Errorf("error: %v", err)
			break
		}
		var msg RawMessage
		err = json.NewDecoder(strings.NewReader(string(message))).Decode(&msg)
		if err != nil {
			logrus.Error(err)
		}
		s, _ := msg.Args.MarshalJSON()
		logrus.Tracef("New Message '%s' from client %d", msg.Type, c.id)
		c.hub.Handle(c, msg.Type, string(s))
		logrus.Tracef("New Message '%s' from client %d handled", msg.Type, c.id)
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) WriteMessage(msg string) error {
	c.send <- []byte(msg)
	return nil
}

func (c *Client) WriteJSON(name string, args interface{}) error {
	var buf bytes.Buffer
	msg := Message{
		Type: name,
	}
	msg.Args = args
	err := json.NewEncoder(&buf).Encode(msg)
	if err != nil {
		return err
	}
	c.send <- buf.Bytes()
	return nil
}
