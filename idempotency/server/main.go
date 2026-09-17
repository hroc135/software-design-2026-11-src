package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

type paymentRequest struct {
	RideID string `json:"ride_id"` // 決済対象の乗車。
	Amount int    `json:"amount"`  // 運賃(円)。
}

type payment struct {
	PaymentID string `json:"payment_id"`
	RideID    string `json:"ride_id"`
	Amount    int    `json:"amount"`
}

// Idempotency-Keyごとに保存する処理結果。
// 処理中に同じキーのリクエストが来た場合はdoneを待って同じ結果を返す。
type result struct {
	done   chan struct{}
	status int
	body   []byte
}

var (
	addr     = flag.String("addr", ":8081", "listen address")
	dropRate = flag.Float64("drop-rate", 0.3,
		"probability of dropping the response after commit")

	mu       sync.Mutex
	payments []payment
	results  = map[string]*result{} // Idempotency-Keyと処理結果の対応。
)

func main() {
	flag.Parse()

	// 決済リクエストを受け付ける。
	http.HandleFunc("POST /payments", paymentsHandler)

	// 決済結果の一覧を返す。
	http.HandleFunc("GET /payments", listPaymentsHandler)

	// 再実験用にサーバーの状態をリセットする。
	http.HandleFunc("POST /reset", resetHandler)

	fmt.Printf("payment server listening on %s (drop-rate=%.0f%%)\n",
		*addr, *dropRate*100)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func paymentsHandler(w http.ResponseWriter, r *http.Request) {
	var req paymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	var res *result
	if key != "" {
		mu.Lock()
		if prev, ok := results[key]; ok {
			mu.Unlock()
			<-prev.done // 処理中なら完了を待つ。
			w.Header().Set("Idempotency-Replayed", "true")
			w.WriteHeader(prev.status)
			w.Write(prev.body)
			log.Printf("replayed key=%s ride=%s", key, req.RideID)
			return
		}
		res = &result{done: make(chan struct{})}
		results[key] = res
		mu.Unlock()
	}

	// 運賃を請求して決済を確定する(=コミット)。
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	p := payment{
		PaymentID: fmt.Sprintf("p_%04d", len(payments)+1),
		RideID:    req.RideID,
		Amount:    req.Amount,
	}
	payments = append(payments, p)
	mu.Unlock()
	log.Printf("charged %s ride=%s amount=%d",
		p.PaymentID, p.RideID, p.Amount)

	body, _ := json.Marshal(p)
	if res != nil {
		res.status = http.StatusOK
		res.body = body
		close(res.done) // 結果を保存してから後続に公開する。
	}

	// コミット後にレスポンスを落とす(疑似障害)。
	if rand.Float64() < *dropRate {
		log.Printf("!! dropping response for ride=%s", req.RideID)
		panic(http.ErrAbortHandler)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func listPaymentsHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"count":    len(payments),
		"payments": payments,
	})
}

func resetHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	payments = nil
	results = map[string]*result{}
	mu.Unlock()
	fmt.Fprintln(w, "reset")
}
