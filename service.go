package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/SvenDH/recs/events"
)

type StoreService interface {
	Subscribe(ctx context.Context, topics ...string) *chan []events.Message
	Unsubscribe(ctx context.Context, sub *chan []events.Message, topics ...string)
	CreateWorld(ctx context.Context, name string) error
	DeleteWorld(ctx context.Context, name string) error
	Create(ctx context.Context, world string, components map[string]interface{}) (uint64, error)
	Delete(ctx context.Context, world string, id uint64) error
	Set(ctx context.Context, world string, id uint64, component string, value string) error
	Remove(ctx context.Context, world string, id uint64, component string) error
	Get(ctx context.Context, world string, id uint64, component string, yield func(uint64, uint64, []byte) bool) error
	Join(nodeID string, addr string) error
}

type Service struct {
	addr   string
	store  StoreService
	t      *template.Template
	logger *log.Logger
}

func NewService(addr string, store StoreService) *Service {
	return &Service{addr: addr, store: store, logger: log.New(os.Stderr, "[http]: ", log.LstdFlags)}
}

func (s *Service) Start() error {
	s.t = template.Must(template.ParseFiles(
		"templates/base.html",
		"templates/index.html",
		"templates/chat.html",
		"templates/message.html",
	))
	fs := http.FileServer(http.Dir("./static"))
	http.HandleFunc("/join", s.handleJoin)
	http.HandleFunc("/events", s.handleEvents)
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/", s.handleKeyRequest)
	go func() {
		log.Fatal(http.ListenAndServe(s.addr, nil))
	}()
	return nil
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}
	url, err := url.Parse(r.URL.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	channels, ok := url.Query()["channel"]
	if !ok {
		http.Error(w, "no topic specified", http.StatusBadRequest)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe before fetching the initial state to avoid missing updates
	ctx := r.Context()
	subscriber := s.store.Subscribe(ctx, channels...)
	defer s.store.Unsubscribe(ctx, subscriber, channels...)

	worldIdx := map[string]uint64{}
	entities := map[string]map[uint64]map[string]interface{}{}
	for _, n := range channels {
		worldIdx[n] = 0
		worldData := map[uint64]map[string]interface{}{}
		s.store.Get(ctx, n, 0, "", func(idx, id uint64, d []byte) bool {
			data := map[string]interface{}{}
			if err = json.Unmarshal(d, &data); err != nil {
				return false
			}
			worldData[id] = data
			if idx != 0 {
				worldIdx[n] = idx
			}
			// Sent initial state
			s.sendMessage(w, events.Message{Channel: n, Op: events.Create, Entity: id, Value: data})
			return true
		})
		entities[n] = worldData
	}
	flusher.Flush()
	for {
		select {
		case msg, ok := <-*subscriber:
			if !ok {
				s.store.Unsubscribe(ctx, subscriber, channels...)
				return
			}
			for _, c := range msg {
				//fmt.Fprintf(w, "event: \"message\"\ndata: %s\n\n", c)
				// Only send the message if it's newer than the last version of the world we sent
				idx, ok := worldIdx[c.Channel]
				if !ok || c.Idx > idx {
					s.sendMessage(w, c)
				}
			}
			flusher.Flush()
		case <-ctx.Done():
			s.store.Unsubscribe(ctx, subscriber, channels...)
			return
		}
	}
}

func (s *Service) handleKeyRequest(w http.ResponseWriter, r *http.Request) {
	getKey := func() (string, string, string) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 2 {
			return "", "", ""
		} else if len(parts) < 3 {
			return parts[1], "", ""
		} else if len(parts) < 4 {
			return parts[1], parts[2], ""
		}
		return parts[1], parts[2], parts[3]
	}
	ctx := r.Context()
	switch r.Method {
	case "GET":
		var err error
		var id uint64 = 0
		n, k, v := getKey()
		if n == "" {
			if err := s.t.Execute(w, nil); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		id, err = strconv.ParseUint(k, 10, 64)
		if v == "" && err != nil {
			v = k
			k = ""
		}
		if k == "" {
			io.WriteString(w, "[")
			s.store.Get(ctx, n, 0, v, func(idx, id uint64, d []byte) bool {
				v := ""
				if idx == 0 {
					v += ","
				}
				_, err := io.WriteString(w, v + string(d))
				return err == nil
			})
			io.WriteString(w, "]")
		} else {
			if s.store.Get(ctx, n, id, v, func(idx, id uint64, d []byte) bool {
				_, err := w.Write(d)
				return err == nil
			}) != nil {
				s.logger.Printf("Failed to get entity: %s", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
			}
		}

	case "POST":
		n, e, c := getKey()
		if n == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if e != "" {
			id, err := strconv.ParseUint(e, 10, 64)
			if err != nil {
				s.logger.Printf("Failed to parse id: %s", err.Error())
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if c == "" {
				m := map[string]interface{}{}
				if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
					s.logger.Printf("Failed to decode request: %s", err.Error())
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				for k, v := range m {
					d, err := json.Marshal(v)
					if err != nil {
						s.logger.Printf("Failed to marshal value: %s", err.Error())
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					err = s.store.Set(ctx, n, id, k, string(d))
					if err != nil {
						s.logger.Printf("Failed to set entity: %s", err.Error())
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
				}
			} else {
				var m string
				if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
					s.logger.Printf("Failed to decode request: %s", err.Error())
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				err := s.store.Set(ctx, n, id, c, m)
				if err != nil {
					s.logger.Printf("Failed to set entity: %s", err.Error())
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
		} else {
			m := map[string]interface{}{}
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
				s.logger.Printf("Failed to decode request: %s", err.Error())
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			ent, err := s.store.Create(ctx, n, m)
			if err != nil {
				s.logger.Printf("Failed to create entity: %s", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			io.WriteString(w, strconv.FormatUint(ent, 10))
		}

	case "DELETE":
		n, k, v := getKey()
		if k == "" {
			w.WriteHeader(http.StatusBadRequest)
		} else {
			id, err := strconv.ParseUint(k, 10, 64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
			} else if v == "" {
				if err := s.store.Delete(ctx, n, id); err != nil {
					s.logger.Printf("Failed to delete entity: %s", err.Error())
					w.WriteHeader(http.StatusInternalServerError)
				}
			} else if err := s.store.Remove(ctx, n, id, v); err != nil {
				s.logger.Printf("Failed to remove component: %s", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
			}
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Service) handleJoin(w http.ResponseWriter, r *http.Request) {
	m := map[string]string{}
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(m) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	remoteAddr, ok := m["addr"]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	nodeID, ok := m["id"]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := s.store.Join(nodeID, remoteAddr); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (s *Service) sendMessage(w http.ResponseWriter, msg events.Message) {
	fmt.Fprintf(w, "event: %s\n\ndata: ", "message")
	s.t.ExecuteTemplate(w, "message.html", msg)
	fmt.Fprintf(w, "\n\n")
}

func (s *Service) renderEntity(w io.Writer, e interface{}) {
	s.t.ExecuteTemplate(w, "entity.html", e)
}