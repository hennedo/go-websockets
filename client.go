// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go_websockets

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/sirupsen/logrus"

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
	id   uint64
}

type RawMessage struct {
	Type string          `json:"type"`
	Args json.RawMessage `json:"args"`
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c.id
	}()
	defer c.conn.Close()

	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		logrus.Tracef("failed setting read deadline: %v", err)
	}
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			logrus.Tracef("failed setting read deadline: %v", err)
		}
		return nil
	})

	for {
		logrus.Tracef("start for loop for client %d", c.id)
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			logrus.Errorf("error: %v", err)
			break
		}

		var msg RawMessage
		err = json.NewDecoder(bytes.NewReader(message)).Decode(&msg)
		if err != nil {
			logrus.Error(err)
		}

		logrus.Tracef("New Message '%s' from client %d", msg.Type, c.id)
		c.hub.Handle(c, msg.Type, msg.Args)
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
	defer ticker.Stop()
	defer c.conn.Close()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				// The hub closed the channel.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				logrus.Tracef("failed getting next writer: %v", err)
				return
			}

			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				logrus.Tracef("failed setting write deadline: %v", err)
				return
			}

			_, _ = w.Write(message)

			if err := w.Close(); err != nil {
				logrus.Tracef("failed closing conn: %v", err)
				return
			}
		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				logrus.Tracef("failed setting write deadline: %v", err)
				return
			}

			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				logrus.Tracef("failed writing message: %v", err)
				return
			}
		}
	}
}

func (c *Client) WriteJSON(name string, args interface{}) (err error) {
	msg := RawMessage{
		Type: name,
	}
	msg.Args, err = json.Marshal(args)
	if err != nil {
		return err
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.send <- msgBytes
	return nil
}
