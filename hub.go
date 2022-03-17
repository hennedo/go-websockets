// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This code is largely copied from the Gorilla Websocket examples.. Some modifications were made :)

package go_websockets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-multierror"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var roomsMetric = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "websockets",
	Subsystem: "hub",
	Name:      "rooms",
	Help:      "All rooms and their sizes",
}, []string{"name"})

var clientsCountMetric = promauto.NewGauge(prometheus.GaugeOpts{
	Namespace: "websockets",
	Subsystem: "hub",
	Name:      "clients",
	Help:      "The current count of clients",
})

var clientsTotalMetric = promauto.NewCounter(prometheus.CounterOpts{
	Namespace: "websockets",
	Subsystem: "hub",
	Name:      "clients_total",
	Help:      "A counter of all ever connected clients",
})

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Callback func(Client, json.RawMessage)

type Hub interface {
	BroadcastRoom(room string, type_ string, args interface{}) error
	Broadcast(type_ string, args interface{}) error
	ServeHTTP(w http.ResponseWriter, r *http.Request)
	On(name string, cb Callback)
}

var (
	_ Hub = (*hub)(nil)
)

// hub maintains the set of active clients and broadcasts messages to the
// clients.
type hub struct {
	roomMtx sync.RWMutex
	rooms   map[string]*hubRoom

	clientMutex sync.RWMutex
	clients     map[uint64]*hubClient

	callbackMutex sync.RWMutex
	callbacks     map[string][]Callback

	lastID       uint64
	onConnect    func(Client)
	onDisconnect func(Client)
}

type Options struct {
	OnConnect    func(Client)
	OnDisconnect func(Client)
}

func NewHub(opt *Options) Hub {
	if opt == nil {
		opt = &Options{}
	}

	h := &hub{
		clients:      make(map[uint64]*hubClient),
		rooms:        make(map[string]*hubRoom),
		callbacks:    make(map[string][]Callback),
		onConnect:    opt.OnConnect,
		onDisconnect: opt.OnDisconnect,
	}
	return h
}

func (h *hub) On(name string, cb Callback) {
	h.callbackMutex.Lock()
	h.callbacks[name] = append(h.callbacks[name], cb)
	h.callbackMutex.Unlock()
}

func (h *hub) handle(client *hubClient, name string, msg json.RawMessage) {
	h.callbackMutex.RLock()
	for _, cb := range h.callbacks[name] {
		cb(client, msg)
	}
	h.callbackMutex.RUnlock()
}

func (h *hub) unregister(c *hubClient) {
	h.clientMutex.Lock()
	if h.onDisconnect != nil {
		h.onDisconnect(c)
	}
	delete(h.clients, c.id)
	h.clientMutex.Unlock()
}

func (h *hub) Broadcast(type_ string, args interface{}) error {
	var group multierror.Group

	h.clientMutex.RLock()
	for _, client := range h.clients {
		// set client again, so the pointer does not get overwritten when it's finally executed..
		client_ := client
		group.Go(func() error {
			return client_.WriteJSON(type_, args)
		})
	}
	h.clientMutex.RUnlock()

	return group.Wait().ErrorOrNil()
}

func (h *hub) BroadcastRoom(room string, type_ string, args interface{}) error {
	h.roomMtx.RLock()

	if r, ok := h.rooms[room]; ok {
		h.roomMtx.RUnlock()
		return r.writeJSON(type_, args)
	}

	h.roomMtx.RUnlock()

	return nil
}

//room count, room names, und ggf all client counts of all rooms (mapped name=>count) bräucht ich da noch :D/

type Metrics struct {
	clients int
	rooms   int
}

func (h *hub) Metrics() Metrics {
	return Metrics{
		clients: len(h.clients),
		rooms:   len(h.rooms),
	}
}

func (h *hub) join(room string, client *hubClient) error {
	h.roomMtx.Lock()

	r, ok := h.rooms[room]
	if !ok {
		r = &hubRoom{
			clients: make(map[uint64]*hubClient),
			lock:    sync.RWMutex{},
		}
		h.rooms[room] = r
	}

	h.roomMtx.Unlock()

	err := r.join(client)
	if err != nil {
		return err
	}
	roomsMetric.WithLabelValues(room).Inc()
	return nil
}

func (h *hub) leave(room string, client *hubClient) error {
	h.roomMtx.RLock()
	r, ok := h.rooms[room]
	h.roomMtx.RUnlock()
	if !ok {
		return fmt.Errorf("room does not exist")
	}

	err := r.leave(client)
	if err != nil {
		return err
	}
	roomsMetric.WithLabelValues(room).Dec()
	if len(r.clients) == 0 {
		h.roomMtx.Lock()
		delete(h.rooms, room)
		h.roomMtx.Unlock()
		roomsMetric.DeleteLabelValues(room)
	}
	return nil
}

// ServeHTTP handles websocket requests from the peer.
func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgrade := false
	for _, header := range r.Header["Upgrade"] {
		if header == "websocket" {
			upgrade = true
			break
		}
	}
	if !upgrade {
		w.WriteHeader(http.StatusOK)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("%s", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	id := atomic.AddUint64(&h.lastID, 1)
	client := &hubClient{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, 256),
		rooms:  make(map[string]bool),
		data:	make(map[string]interface{}),
		id:     id,
		ctx:    ctx,
		cancel: cancel,
	}
	h.clientMutex.Lock()
	h.clients[id] = client
	h.clientMutex.Unlock()
	clientsTotalMetric.Inc()
	clientsCountMetric.Inc()

	if h.onConnect != nil {
		h.onConnect(client)
	}

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
