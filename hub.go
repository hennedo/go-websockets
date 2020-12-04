// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package main

import (
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"net/http"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	Clients    map[int]*Client
	Callbacks  map[string][]func(*Client, string)
	lastId     int
	unregister chan int
}

func NewHub() *Hub {
	h := &Hub{
		Clients:    map[int]*Client{},
		Callbacks:  map[string][]func(*Client, string){},
		lastId:     0,
		unregister: make(chan int),
	}
	go h.run()
	return h

}

func (h *Hub) On(name string, cb func(*Client, string)) {
	if _, found := h.Callbacks[name]; !found {
		h.Callbacks[name] = []func(*Client, string){cb}
	} else {
		h.Callbacks[name] = append(h.Callbacks[name], cb)
	}
}

func (h *Hub) Handle(client *Client, name string, msg string) {
	if h.Callbacks[name] != nil {
		for _, cb := range h.Callbacks[name] {
			cb(client, msg)
		}
	}
}

func (h *Hub) run() {
	for {
		select {
		case id := <-h.unregister:
			if client, ok := h.Clients[id]; ok {
				delete(h.Clients, id)
				close(client.send)
			}
		}
	}
}

func (h *Hub) Broadcast(msg string) {
	for _, client := range h.Clients {
		err := client.WriteMessage(msg)
		if err != nil {
			logrus.Error(err)
		}
	}
}

func (h *Hub) BroadcastJSON(name string, args interface{}) {
	for _, client := range h.Clients {
		err := client.WriteJSON(name, args)
		if err != nil {
			logrus.Error(err)
		}
	}
}

// serveWs handles websocket requests from the peer.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logrus.Error(err)
		return
	}
	id := h.lastId
	h.lastId++
	client := &Client{hub: h, conn: conn, send: make(chan []byte, 256), id: id}
	h.Clients[id] = client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
