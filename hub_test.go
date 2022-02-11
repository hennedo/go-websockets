package go_websockets

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHub(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		hub := NewHub()

		hub.On("foo", func(client Client, message json.RawMessage) {
			err := client.WriteJSON("bar", nil)
			if err != nil {
				t.Fatal(err)
			}
		})
		server := httptest.NewServer(http.HandlerFunc(hub.ServeWs))
		defer server.Close()

		var wg sync.WaitGroup
		client, err := newClient("ws://"+server.Listener.Addr().String(), func(bytes []byte) {
			defer wg.Done()

			log.Println(string(bytes))
		})
		if err != nil {
			t.Fatal(err)
		}

		wg.Add(1)
		if err := client.sendMessage(OutgoingMessage{Type: "foo", Args: nil}); err != nil {
			t.Fatal(err)
		}

		wg.Wait()
		client.Close()
	})
}

type wsClient struct {
	conn   *websocket.Conn
	onMsg  func([]byte)
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (w *wsClient) sendMessage(m OutgoingMessage) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}

	if err := w.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("during writing to websocket: %v", err)
	}

	return nil
}

func (w *wsClient) receiveHandler() {
	defer w.cancel()
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		_, msg, err := w.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return
			}
			log.Fatalf("receive: %v", err)
		}

		w.onMsg(msg)
	}
}

func (w *wsClient) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			// Close our websocket connection
			err := w.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Printf("during closing websocket: %v", err)
			}

			return
		}
	}
}

func (w *wsClient) Close() error {
	w.cancel()
	w.wg.Wait()
	return w.conn.Close()
}

func newClient(uri string, onMsg func([]byte)) (*wsClient, error) {
	conn, resp, err := websocket.DefaultDialer.Dial(uri, nil)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("invalid status code: %v != %v", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &wsClient{
		conn:   conn,
		onMsg:  onMsg,
		ctx:    ctx,
		cancel: cancel,
	}
	w.wg.Add(2)
	go w.receiveHandler()
	go w.run()

	return w, nil
}
