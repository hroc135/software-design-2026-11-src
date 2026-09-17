package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

func main() {
	base := flag.String("base", "http://localhost:8081", "server base URL")
	payments := flag.Int("payments", 10, "number of payment requests")
	amount := flag.Int("amount", 1500, "fare per ride (JPY)")
	useKey := flag.Bool("use-key", false, "send Idempotency-Key header")
	flag.Parse()

	// keep-alive接続の再利用時にhttp.Transportがリクエストを自動再送する
	// ことがあり、デモの見通しが悪くなるため無効化しておく。
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	for i := 1; i <= *payments; i++ {
		rideID := fmt.Sprintf("ride-%03d", i)
		body, _ := json.Marshal(map[string]any{
			"ride_id": rideID,
			"amount":  *amount,
		})
		key := uuid.NewString() // この決済を識別する一意なキー。

		err := requestPaymentWithRetry(client, *base+"/payments", body,
			key, *useKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: give up: %v\n", rideID, err)
		}
	}

	// 最終的な決済結果を確認する。
	resp, err := client.Get(*base + "/payments")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var summary struct {
		Count int `json:"count"`
	}
	json.NewDecoder(resp.Body).Decode(&summary)

	fmt.Printf("決済依頼数=%d 決済数=%d\n", *payments, summary.Count)
	if summary.Count != *payments {
		fmt.Println("=> 多重決済が発生している!")
	} else {
		fmt.Println("=> exactly once を実現できている")
	}
}

const (
	maxRetries  = 5
	baseBackoff = 100 * time.Millisecond
	maxBackoff  = 2 * time.Second
)

func requestPaymentWithRetry(client *http.Client, url string, body []byte,
	key string, useKey bool) error {

	backoff := baseBackoff
	var lastErr error
	for i := range maxRetries + 1 {
		if i > 0 {
			time.Sleep(rand.N(backoff))
			backoff = min(backoff*2, maxBackoff)
		}

		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if useKey {
			req.Header.Set("Idempotency-Key", key)
		}

		resp, err := client.Do(req)
		if err != nil {
			// 決済が確定済みか不明なままリトライする(at-least-once)。
			lastErr = err
			fmt.Printf("attempt %d failed: %v -> retry\n", i+1, err)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			replayed := ""
			if resp.Header.Get("Idempotency-Replayed") == "true" {
				replayed = " (replayed)"
			}
			fmt.Printf("attempt %d ok: %s%s\n",
				i+1, bytes.TrimSpace(respBody), replayed)
			return nil
		}
		lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, respBody)
		if resp.StatusCode >= 500 {
			fmt.Printf("attempt %d failed: %v -> retry\n", i+1, lastErr)
			continue
		}
		return lastErr // 4xxはリトライしない。
	}
	return lastErr
}
