package go_websockets

import (
	"sync"
)

type Room struct {
	clients map[uint64]*Client
	lock sync.RWMutex
}

func (r *Room) join(client *Client) {
	r.lock.Lock()
	r.clients[client.id] = client
	r.lock.Unlock()
}

func (r *Room) leave(client *Client) {
	r.lock.Lock()
	delete(r.clients, client.id)
	r.lock.Unlock()
}

func (r *Room) WriteJSON(name string, args interface{}) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	for _, client := range r.clients {
		client.WriteJSON(name, args)
	}
}
