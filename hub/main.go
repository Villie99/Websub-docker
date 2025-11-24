package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Subscriber struct {
	CallbackURL, Secret, Topic string
}

type Hub struct {
	subscribers map[string][]Subscriber
	mu          sync.RWMutex
}

func main() {
	fmt.Println("Starting the WebSub Hub server now")
	hub := &Hub{subscribers: make(map[string][]Subscriber)}

	http.HandleFunc("/", hub.handleWebSub)
	http.HandleFunc("/subscribe", hub.handleWebSub)
	http.HandleFunc("/publish", hub.handlePublish)
	http.HandleFunc("/health", hub.handleHealth)

	log.Println("Hub is now started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (hub *Hub) handleWebSub(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		return
	}

	callback, topic, mode, secret := hub.parseParams(r)
	if callback == "" || topic == "" || mode == "" {
		if r.URL.Path == "/" {
			json.NewEncoder(w).Encode(map[string]string{"status": "WebSub Hub running"})
			return
		}
		http.Error(w, "Missing parameters", http.StatusBadRequest)
		return
	}

	if mode != "subscribe" && mode != "unsubscribe" {
		http.Error(w, "Wroing with mode", http.StatusBadRequest)
		return
	}

	if !hub.verifyIntent(callback, topic, mode) {
		http.Error(w, "Wrong with verification", http.StatusBadRequest)
		return
	}

	hub.updateSubscribers(topic, callback, secret, mode == "subscribe")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(generateChallenge()))

	log.Printf("Subscription: %s for topic %s", callback, topic)
}

func (hub *Hub) parseParams(r *http.Request) (string, string, string, string) {
	if r.Method == "POST" {
		r.ParseForm()
		if callback := r.FormValue("hub.callback"); callback != "" {
			return callback, r.FormValue("hub.topic"), r.FormValue("hub.mode"), r.FormValue("hub.secret")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		return body["hub.callback"], body["hub.topic"], body["hub.mode"], body["hub.secret"]
	}
	q := r.URL.Query()
	return q.Get("hub.callback"), q.Get("hub.topic"), q.Get("hub.mode"), q.Get("hub.secret")
}

func (hub *Hub) verifyIntent(callback, topic, mode string) bool {
	challenge := generateChallenge()
	verifyURL, _ := url.Parse(callback)
	q := verifyURL.Query()
	q.Set("hub.mode", mode)
	q.Set("hub.topic", topic)
	q.Set("hub.challenge", challenge)
	verifyURL.RawQuery = q.Encode()

	resp, err := http.Get(verifyURL.String())
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return string(body) == challenge
}

func (hub *Hub) updateSubscribers(topic, callback, secret string, subscribe bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	subs := hub.subscribers[topic]
	if subscribe {
		for i := range subs {
			if subs[i].CallbackURL == callback {
				subs[i].Secret = secret
				return
			}
		}
		hub.subscribers[topic] = append(subs, Subscriber{callback, secret, topic})
		log.Printf("Subscribed to %s: %s", topic, callback)
	} else {
		for i, sub := range subs {
			if sub.CallbackURL == callback {
				hub.subscribers[topic] = append(subs[:i], subs[i+1:]...)
				log.Printf("Unsubscribed from %s: %s", topic, callback)
				return
			}
		}
	}
}

func (hub *Hub) handlePublish(w http.ResponseWriter, r *http.Request) {
	log.Printf("Publish request: %s %s", r.Method, r.URL.Path)

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Message string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	topic := "test-topic"

	hub.mu.RLock()
	subs := hub.subscribers[topic]
	hub.mu.RUnlock()

	log.Printf("Publishing to topic '%s' with %d subscribers. Message: '%s'", topic, len(subs), req.Message)

	content := map[string]interface{}{
		"message":   req.Message,
		"timestamp": time.Now().Format(time.RFC3339),
		"topic":     topic,
	}

	contentBytes, _ := json.Marshal(content)

	for _, sub := range subs {
		go hub.notifySubscriber(sub, contentBytes)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "published",
		"message":     req.Message,
		"subscribers": len(subs),
		"topic":       topic,
	})
}

func (hub *Hub) notifySubscriber(sub Subscriber, content []byte) {
	req, _ := http.NewRequest("POST", sub.CallbackURL, bytes.NewReader(content))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Link", `<http://hub:8080>; rel="hub", <`+sub.Topic+`>; rel="self"`)

	if sub.Secret != "" {
		mac := hmac.New(sha256.New, []byte(sub.Secret))
		mac.Write(content)
		req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (hub *Hub) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK"))
}

func generateChallenge() string {
	return fmt.Sprintf("challenge %d", time.Now().UnixNano())
}
