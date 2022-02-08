// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go_websockets

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-multierror"
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
	roomMtx    sync.RWMutex
	clients    map[uint64]*Client
	rooms      map[interface{}]*Room
	callbacks  map[string][]Callback
	lastID     uint64
	unregister chan uint64
}

func (h *Hub) ConnectedClientsCount() (result int) {
	h.mtx.RLock()
	clients := len(h.clients)
	h.mtx.RUnlock()
	return clients
}

func (h *Hub) RoomClientsCount(name string) (result int) {
	h.mtx.RLock()
	var clients int
	if h.rooms[name] != nil {
		clients = len(h.rooms[name].clients)
	}
	h.mtx.RUnlock()
	return clients
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
				if client.send != nil {
					close(client.send)
				}
				if client.read != nil {
					close(client.read)
				}
			}
		}
	}
}

func (h *Hub) Broadcast(name string, args interface{}) (result error) {
	h.mtx.RLock()
	for _, client := range h.clients {
		if client == nil {
			continue
		}
		h.mtx.RUnlock()
		if err := client.WriteJSON(name, args); err != nil {
			result = multierror.Append(result, err)
		}
		h.mtx.RLock()
	}
	h.mtx.RUnlock()

	return result
}

func (h *Hub) BroadcastRoom(name string, room interface{}, args interface{}) (result error) {
	h.roomMtx.RLock()
	r, ok := h.rooms[room]
	if !ok {
		h.roomMtx.RUnlock()
		return errors.New("room does not exist")
	}
	h.roomMtx.RUnlock()
	r.WriteJSON(name, args)
	return result
}

func (h *Hub) join(room interface{}, client *Client) {
	fmt.Printf("start joining room %v\n", room)
	h.roomMtx.Lock()
	defer h.roomMtx.Unlock()
	_, ok := h.rooms[room]
	if !ok {
		fmt.Printf("creating room %v\n", room)
		h.rooms[room] = &Room{
			clients: make(map[uint64]*Client),
			lock:    sync.RWMutex{},
		}
	}
	fmt.Printf("joining room %v\n", room)
	h.rooms[room].join(client)
}

func (h *Hub) leave(room interface{}, client *Client) {
	h.roomMtx.RLock()
	r, ok := h.rooms[room]
	h.roomMtx.RUnlock()
	if !ok {
		return
	}
	r.leave(client)
	if len(h.rooms[room].clients) == 0 {
		h.roomMtx.Lock()
		delete(h.rooms, room)
		h.roomMtx.Unlock()
	}
}

// ServeWs handles websocket requests from the peer.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request) {
	upgrade := false
	for _, header := range r.Header["Upgrade"] {
		if header == "websocket" {
			upgrade = true
			break
		}
	}
	if !upgrade {
		w.WriteHeader(200)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("%s", err)
		return
	}

	id := atomic.AddUint64(&h.lastID, 1)
	client := &Client{
		hub:   h,
		conn:  conn,
		send:  make(chan []byte, 256),
		rooms: make(map[interface{}]struct{}),
		id:    id,
	}
	h.mtx.Lock()
	h.clients[id] = client
	h.mtx.Unlock()

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
