package go_websockets

import (
	"github.com/hashicorp/go-multierror"
	"sync"
)

type hubRoom struct {
	clients map[uint64]*hubClient
	lock    sync.RWMutex
}

func (r *hubRoom) join(client *hubClient) {
	r.lock.Lock()
	r.clients[client.id] = client
	r.lock.Unlock()
}

func (r *hubRoom) leave(client *hubClient) {
	r.lock.Lock()
	delete(r.clients, client.id)
	r.lock.Unlock()
}

func (r *hubRoom) clientsCount() int {
	return len(r.clients)
}

func (r *hubRoom) writeJSON(type_ string, args interface{}) error {
	var group multierror.Group

	r.lock.RLock()
	for _, client := range r.clients {
		group.Go(func() error {
			return client.WriteJSON(type_, args)
		})
	}
	r.lock.RUnlock()

	return group.Wait().ErrorOrNil()
}
