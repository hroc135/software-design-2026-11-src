package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 503時にクライアントへ指示する待ち時間(秒)。
const retryAfterSecs = 3

var (
	addr     = flag.String("addr", ":8080", "listen address")
	capacity = flag.Int("capacity", 20, "max concurrent requests")
	latency  = flag.Duration("latency", 100*time.Millisecond,
		"processing time per request")

	total    atomic.Int64
	ok       atomic.Int64
	rejected atomic.Int64

	mu          sync.Mutex
	outageUntil time.Time

	sem    chan struct{}
	reject func(http.ResponseWriter, string)
)

func main() {
	flag.Parse()

	sem = make(chan struct{}, *capacity)

	reject = func(w http.ResponseWriter, msg string) {
		rejected.Add(1)
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSecs))
		http.Error(w, msg, http.StatusServiceUnavailable)
	}

	// 決済リクエストを受け付ける。
	http.HandleFunc("POST /payments", paymentsHandler)

	// 疑似的な障害を注入する: curl 'localhost:8080/outage?d=5s' のように使う。
	http.HandleFunc("/outage", outageHandler)

	// 毎秒、受信リクエスト数を表示する。
	go func() {
		var prevTotal, prevOK, prevRej int64
		for range time.Tick(time.Second) {
			t, o, rj := total.Load(), ok.Load(), rejected.Load()
			dt, do, dr := t-prevTotal, o-prevOK, rj-prevRej
			prevTotal, prevOK, prevRej = t, o, rj
			bar := strings.Repeat("#", int(min((dt+9)/10, 40))) // #1つで10件を表す（端数は切り上げ）。
			fmt.Printf("%s req/s=%4d ok=%4d 503=%4d %s\n",
				time.Now().Format("15:04:05"), dt, do, dr, bar)
		}
	}()

	fmt.Printf("listening on %s (capacity=%d, latency=%s, retry-after=%ds)\n",
		*addr, *capacity, *latency, retryAfterSecs)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// paymentsHandler は決済リクエストを処理し、決済IDを返す。
func paymentsHandler(w http.ResponseWriter, r *http.Request) {
	total.Add(1)

	if inOutage() {
		reject(w, "outage")
		return
	}

	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default: // 容量オーバー。
		reject(w, "overloaded")
		return
	}

	time.Sleep(*latency) // 決済処理に相当する時間。
	fmt.Fprintf(w, `{"payment_id":"p_%04d"}`+"\n", ok.Add(1))
}

// inOutage は疑似障害の注入期間中かどうかを返す。
func inOutage() bool {
	mu.Lock()
	defer mu.Unlock()
	return time.Now().Before(outageUntil)
}

func outageHandler(w http.ResponseWriter, r *http.Request) {
	d, err := time.ParseDuration(r.URL.Query().Get("d"))
	if err != nil {
		http.Error(w, "usage: /outage?d=5s", http.StatusBadRequest)
		return
	}
	mu.Lock()
	outageUntil = time.Now().Add(d)
	mu.Unlock()
	fmt.Printf("--- outage injected for %s ---\n", d)
	time.AfterFunc(d, func() {
		if !inOutage() { // 別の障害注入で延長された場合は出力しない。
			fmt.Println("--- outage ended ---")
		}
	})
	fmt.Fprintf(w, "outage for %s\n", d)
}
