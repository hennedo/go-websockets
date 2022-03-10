package go_websockets

import (
	"fmt"
	"github.com/hashicorp/go-multierror"
	"sync"
)

type hubRoom struct {
	clients map[uint64]*hubClient
	lock    sync.RWMutex
}

func (r *hubRoom) join(client *hubClient) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.clients[client.id]; ok {
		return fmt.Errorf("client already joined")
	}
	r.clients[client.id] = client
	return nil
}

func (r *hubRoom) leave(client *hubClient) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.clients[client.id]; !ok {
		return fmt.Errorf("client not joined")
	}
	delete(r.clients, client.id)
	return nil
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
