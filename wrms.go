package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"nhooyr.io/websocket"
)

type Song struct {
	Title     string              `json:"title"`
	Artist    string              `json:"artist"`
	Source    string              `json:"source"`
	Uri       string              `json:"uri"`
	Weight    int                 `json:"weight"`
	index     int                 `json:"-"` // used by heap.Interface
	Upvotes   map[string]struct{} `json:"-"`
	Downvotes map[string]struct{} `json:"-"`
}

func NewSong(title, artist, source, uri string) Song {
	return Song{title, artist, source, uri, 0, 0, map[string]struct{}{}, map[string]struct{}{}}
}

func NewSongFromJson(data []byte) (Song, error) {
	var s Song
	err := json.Unmarshal(data, &s)
	if err != nil {
		log.Println(fmt.Sprintf("Failed to parse song data %s with %s", string(data), err))
		return s, err
	}

	s.Upvotes = map[string]struct{}{}
	s.Downvotes = map[string]struct{}{}
	return s, nil
}

type Event struct {
	Event string `json:"cmd"`
	Songs []Song `json:"songs"`
}

type Connection struct {
	Id     uuid.UUID
	Events chan Event
	C      *websocket.Conn
}

func (c *Connection) Close() {
	log.Println("Closing connection %s", c.Id)
	close(c.Events)
	c.C.Close(websocket.StatusNormalClosure, "")
}

type Wrms struct {
	Connections []Connection
	Songs       []Song
	Queue       Playlist
	CurrentSong Song
	Player      Player
	Playing     bool
}

func NewWrms() Wrms {
	wrms := Wrms{}
	wrms.Player = NewPlayer(&wrms)
	return wrms
}

func (wrms *Wrms) Close() {
	log.Println("Closing WRMS")
	for _, conn := range wrms.Connections {
		conn.Close()
	}
}

func (wrms *Wrms) Broadcast(cmd Event) {
	for i := 0; i < len(wrms.Connections); i++ {
		wrms.Connections[i].Events <- cmd
	}
}

func (wrms *Wrms) AddSong(song Song) {
	wrms.Songs = append(wrms.Songs, song)
	s := &wrms.Songs[len(wrms.Songs)-1]
	wrms.Queue.Add(s)
	log.Println(fmt.Sprintf("Added song %s (ptr=%p) to Songs", s.Uri, s))
}

func (wrms *Wrms) Next() {
	next := wrms.Queue.PopSong()
	if next == nil {
		wrms.CurrentSong.Uri = ""
		return
	}

	wrms.CurrentSong = *next

	for i, s := range wrms.Songs {
		if s.Uri == wrms.CurrentSong.Uri {
			wrms.Songs[i] = wrms.Songs[len(wrms.Songs)-1]
			wrms.Songs = wrms.Songs[:len(wrms.Songs)-1]
			break
		}
	}
}

func (wrms *Wrms) PlayPause() {
	var ev Event
	if !wrms.Playing {
		if wrms.CurrentSong.Uri == "" {
			log.Println("No song currently playing play the next")
			wrms.Next()
		}
		ev = Event{"play", []Song{wrms.CurrentSong}}
		wrms.Player.Play(&wrms.CurrentSong)
	} else {
		ev = Event{"pause", []Song{}}
		wrms.Player.Pause()
	}

	wrms.Playing = !wrms.Playing

	wrms.Broadcast(ev)
}

func (wrms *Wrms) AdjustSongWeight(connId string, songUri string, vote string) {
	for i := 0; i < len(wrms.Songs); i++ {
		s := &wrms.Songs[i]
		if s.Uri != songUri {
			continue
		}

		log.Println(fmt.Sprintf("Adjusting song %s (ptr=%p)", s.Uri, s))
		switch vote {
		case "up":
			if _, ok := s.Upvotes[connId]; ok {
				log.Println(fmt.Sprintf("Double upvote of song %s by connections %s", songUri, connId))
				return
			}

			if _, ok := s.Downvotes[connId]; ok {
				delete(s.Downvotes, connId)
				s.Weight += 2
			} else {
				s.Weight += 1
			}

			s.Upvotes[connId] = struct{}{}

		case "down":
			if _, ok := s.Downvotes[connId]; ok {
				log.Println(fmt.Sprintf("Double downvote of song %s by connections %s", songUri, connId))
				return
			}

			if _, ok := s.Upvotes[connId]; ok {
				delete(s.Upvotes, connId)
				s.Weight -= 2
			} else {
				s.Weight -= 1
			}

			s.Downvotes[connId] = struct{}{}

		case "unvote":
			if _, ok := s.Downvotes[connId]; ok {
				delete(s.Downvotes, connId)
				s.Weight += 1
			}

			if _, ok := s.Upvotes[connId]; ok {
				delete(s.Upvotes, connId)
				s.Weight -= 1
			} else {
				log.Println(fmt.Sprintf("Double downvote of song %s by connections %s", songUri, connId))
				return
			}

		default:
			log.Fatal("invalid vote")
		}

		wrms.Queue.Adjust(s)
		wrms.Broadcast(Event{"update", []Song{*s}})
		break
	}
}
