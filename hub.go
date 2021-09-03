// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go_websockets

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-multierror"
	"github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Callback func(*Client, json.RawMessage)

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	mtx        sync.RWMutex
	clients    map[uint64]*Client
	callbacks  map[string][]Callback
	lastID     uint64
	unregister chan uint64
}

func NewHub() *Hub {
	h := &Hub{
		clients:    make(map[uint64]*Client),
		callbacks:  make(map[string][]Callback),
		unregister: make(chan uint64),
	}
	go h.run()
	return h
}

func (h *Hub) On(name string, cb Callback) {
	h.callbacks[name] = append(h.callbacks[name], cb)
}

func (h *Hub) Handle(client *Client, name string, msg json.RawMessage) {
	for _, cb := range h.callbacks[name] {
		cb(client, msg)
	}
}

func (h *Hub) run() {
	for {
		select {
		case id := <-h.unregister:
			h.mtx.RLock()
			client, ok := h.clients[id]
			h.mtx.RUnlock()
			if ok {
				h.mtx.Lock()
				delete(h.clients, id)
				h.mtx.Unlock()

				close(client.send)
			}
		}
	}
}

func (h *Hub) BroadcastJSON(name string, args interface{}) (result error) {
	h.mtx.RLock()
	for _, client := range h.clients {
		h.mtx.RUnlock()
		if err := client.WriteJSON(name, args); err != nil {
			result = multierror.Append(result, err)
		}
		h.mtx.RLock()
	}
	h.mtx.RUnlock()

	return result
}

// ServeWs handles websocket requests from the peer.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logrus.Error(err)
		return
	}

	id := atomic.AddUint64(&h.lastID, 1)
	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
		id:   id,
	}
	h.mtx.Lock()
	h.clients[id] = client
	h.mtx.Unlock()

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
