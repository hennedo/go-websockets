// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go_websockets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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

type Client interface {
	io.Closer
	Join(room string) error
	Leave(room string) error
	WriteJSON(name string, args interface{}) error
	Context() context.Context

	// SetData allows setting arbitrary data for the client
	// as example for session management
	SetData(key string, value interface{})

	// GetData allows fetching arbitrary data for the client
	// as example for session management
	GetData(key string) interface{}

	// Equals allows for comparing clients
	Equals(client Client) bool

	GetID() uint64
}

var (
	_ Client = (*hubClient)(nil)
)

// hubClient is a middleman between the websocket connection and the hub.
type hubClient struct {
	hub    *hub
	conn   *websocket.Conn
	send   chan []byte
	ctx    context.Context
	cancel context.CancelFunc

	id uint64

	roomsMtx sync.RWMutex
	rooms    map[string]bool

	dataMtx sync.RWMutex
	data    map[string]interface{}
}

func (c *hubClient) Context() context.Context {
	return c.ctx
}

func (c *hubClient) Equals(client Client) bool {
	return c.id == client.GetID()
}

func (c *hubClient) GetID() uint64 {
	return c.id
}

func (c *hubClient) SetData(key string, value interface{}) {
	c.dataMtx.Lock()
	c.data[key] = value
	c.dataMtx.Unlock()
}

func (c *hubClient) GetData(key string) interface{} {
	c.dataMtx.RLock()
	defer c.dataMtx.RUnlock()
	return c.data[key]
}

func (c *hubClient) Close() error {
	c.cancel()

	c.roomsMtx.Lock()
	for room := range c.rooms {
		err := c.hub.leave(room, c)
		if err != nil {
			fmt.Printf("error leaving room on client close: %v\n", err)
		}
		// even if we're closing this client, prevent exiting from rooms multiple times
		delete(c.rooms, room)
	}
	c.roomsMtx.Unlock()

	c.hub.unregister(c)

	return c.conn.Close()
}

func (c *hubClient) Join(room string) error {
	err := c.hub.join(room, c)
	if err != nil {
		return err
	}

	c.roomsMtx.Lock()
	defer c.roomsMtx.Unlock()
	c.rooms[room] = true
	return nil
}

func (c *hubClient) Leave(room string) error {
	err := c.hub.leave(room, c)
	if err != nil {
		return err
	}

	c.roomsMtx.Lock()
	defer c.roomsMtx.Unlock()
	delete(c.rooms, room)
	return nil
}

type IncomingMessage struct {
	Type string          `json:"type"`
	Args json.RawMessage `json:"args"`
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *hubClient) readPump() {
	defer c.Close()

	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		log.Printf("failed setting read deadline: %v", err)
	}
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			return fmt.Errorf("failed setting read deadline: %v", err)
		}
		return nil
	})

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}

			if errors.Is(err, net.ErrClosed) {
				return
			}

			log.Printf("reading message: %v", err)
			return
		}

		var msg IncomingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			fmt.Printf("decoding message: %s", err)
			continue
		}

		c.hub.handle(c, msg.Type, msg.Args)
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *hubClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer close(c.send)
	defer c.Close()
	defer clientsCountMetric.Dec()

	for {
		select {
		case <-c.ctx.Done():
			// The hub closed the channel.
			_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return

		case message := <-c.send:
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
				log.Printf("failed setting write deadline: %v", err)
				return
			}

			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("failed writing message: %v", err)
				return
			}
		}
	}
}

type OutgoingMessage struct {
	Type string      `json:"type"`
	Args interface{} `json:"args"`
}

func (c *hubClient) WriteJSON(type_ string, args interface{}) (err error) {
	msgBytes, err := json.Marshal(OutgoingMessage{
		Type: type_,
		Args: args,
	})
	if err != nil {
		return err
	}

	defer func() {
		if e := recover(); e != nil {
			if err, ok := e.(error); ok && err.Error() == "send on closed channel" {
				return
			}
			err = fmt.Errorf("recovered from exception in WriteJSON: %v", e)
		}
	}()

	c.send <- msgBytes
	return nil
}
