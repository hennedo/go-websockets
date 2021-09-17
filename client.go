// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go_websockets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
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
	read chan struct{}
	rooms map[interface{}]struct{}
	roomsMtx sync.RWMutex
	id   uint64
	Data	interface{}
}

type RawMessage struct {
	Type string          `json:"type"`
	Args json.RawMessage `json:"args"`
}

func (c *Client) Close() {
	c.conn.Close()
	c.roomsMtx.Lock()
	for room := range c.rooms {
		c.hub.leave(room, c)
	}
	c.roomsMtx.Unlock()
	c.hub.unregister <- c.id
}

func (c *Client) Join(room interface{}) {
	c.roomsMtx.Lock()
	defer c.roomsMtx.Unlock()
	c.hub.join(room, c)
	c.rooms[room] = struct{}{}
}

func (c *Client) Leave(room interface{}) {
	c.roomsMtx.Lock()
	defer c.roomsMtx.Unlock()
	c.hub.leave(room, c)
	delete(c.rooms, room)
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer c.Close()

	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		fmt.Errorf("failed setting read deadline: %v", err)
	}
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			fmt.Errorf("failed setting read deadline: %v", err)
		}
		return nil
	})

	for {
		select {
			case _, ok := <- c.read:
				if !ok {
					return
				}
			default:
		}
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			fmt.Errorf("error: %v", err)
			break
		}

		var msg RawMessage
		err = json.NewDecoder(bytes.NewReader(message)).Decode(&msg)
		if err != nil {
			fmt.Errorf("%s", err)
		}

		c.hub.Handle(c, msg.Type, msg.Args)
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
	defer c.Close()

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
				return
			}

			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}

			_, _ = w.Write(message)

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				fmt.Errorf("failed setting write deadline: %v", err)
				return
			}

			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				fmt.Errorf("failed writing message: %v", err)
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
