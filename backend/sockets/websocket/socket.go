/*
 *  Glue - Robust Go and Javascript Socket Library
 *  Copyright (C) 2015  Roland Singer <roland.singer[at]desertbit.com>
 *
 *  This program is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  This program is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package websocket

import (
	"io"
	"strings"
	"time"

	"github.com/desertbit/glue/backend/closer"
	"github.com/desertbit/glue/backend/global"
	"github.com/desertbit/glue/log"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/websocket"
)

//#################//
//### Constants ###//
//#################//

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next message from the peer.
	readWait = 60 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 0
)

//######################//
//### WebSocket type ###//
//######################//

type Socket struct {
	ws *websocket.Conn

	closer  *closer.Closer
	onClose func()

	writeChan chan string
	readChan  chan string

	userAgent      string
	remoteAddrFunc func() string
}

// Create a new websocket value.
func newSocket(ws *websocket.Conn) *Socket {
	w := &Socket{
		ws:        ws,
		writeChan: make(chan string, global.WriteChanSize),
		readChan:  make(chan string, global.ReadChanSize),
	}

	// Set the closer function.
	w.closer = closer.New(func() {
		// Send a close message to the client.
		// Ignore errors.
		w.write(websocket.CloseMessage, []byte{})

		// Close the socket.
		w.ws.Close()

		// Trigger the onClose function if defined.
		if w.onClose != nil {
			w.onClose()
		}
	})

	return w
}

//############################################//
//### WebSocket - Interface implementation ###//
//############################################//

func (w *Socket) Type() global.SocketType {
	return global.TypeWebSocket
}

func (w *Socket) RemoteAddr() string {
	return w.remoteAddrFunc()
}

func (w *Socket) UserAgent() string {
	return w.userAgent
}

func (w *Socket) Close() {
	w.closer.Close()
}

func (w *Socket) OnClose(f func()) {
	w.onClose = f
}

func (w *Socket) IsClosed() bool {
	return w.closer.IsClosed()
}

func (w *Socket) WriteChan() chan string {
	return w.writeChan
}

func (w *Socket) ReadChan() chan string {
	return w.readChan
}

//###########################//
//### WebSocket - Private ###//
//###########################//

// write writes a message with the given message type and payload.
func (w *Socket) write(mt int, payload []byte) error {
	w.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return w.ws.WriteMessage(mt, payload)
}

// readLoop reads messages from the websocket
func (w *Socket) readLoop() {
	defer func() {
		// Close the socket on defer.
		w.Close()
	}()

	// Set the limits.
	w.ws.SetReadLimit(maxMessageSize)

	// Set the pong handler.
	w.ws.SetPongHandler(func(string) error {
		// Reset the read deadline.
		w.ws.SetReadDeadline(time.Now().Add(readWait))
		return nil
	})

	for {
		// Reset the read deadline.
		w.ws.SetReadDeadline(time.Now().Add(readWait))

		// Read from the websocket.
		_, data, err := w.ws.ReadMessage()
		if err != nil {
			// Only log errors if this is not EOF and
			// if the socket was not closed already.
			// The gorilla socket returns the following string if the socket is
			// closed and the read message request fails: "websocket: close 1001 "
			// Currently there is no better solution to determind this, by comparing
			// the error string message.
			// TODO: Provide a patch to the gorilla websocket package to solve this dirty hack.
			if err != io.EOF && !w.IsClosed() &&
				strings.TrimSpace(err.Error()) != "websocket: close 1001" {
				// Log
				log.L.WithFields(logrus.Fields{
					"remoteAddress": w.RemoteAddr(),
					"userAgent":     w.UserAgent(),
				}).Warningf("failed to read data from websocket: %v", err)
			}

			// Return and release this goroutine.
			// This will close this socket connection.
			return
		}

		// Write the received data to the read channel.
		w.readChan <- string(data)
	}
}

func (w *Socket) writeLoop() {
	for {
		select {
		case data := <-w.writeChan:
			// Write the data to the websocket.
			err := w.write(websocket.TextMessage, []byte(data))
			if err != nil {
				log.L.WithFields(logrus.Fields{
					"remoteAddress": w.RemoteAddr(),
					"userAgent":     w.UserAgent(),
				}).Warningf("failed to write to websocket: %v", err)

				// Close the websocket on error.
				w.Close()
				return
			}

		case <-w.closer.IsClosedChan:
			// Just release this loop.
			return
		}
	}
}
