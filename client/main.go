package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// 指数バックオフの初期値と上限。
const (
	baseBackoff = 100 * time.Millisecond
	maxBackoff  = 3 * time.Second
)

var attempts atomic.Int64

func main() {
	url := flag.String("url", "http://localhost:8080/payments", "target URL")
	workers := flag.Int("workers", 60, "number of concurrent workers")
	duration := flag.Duration("duration", 30*time.Second, "how long to run")
	maxRetries := flag.Int("max-retries", 10, "max retries per payment")
	backoff := flag.Bool("backoff", false,
		"use exponential backoff with jitter")
	flag.Parse()

	var success, giveup atomic.Int64
	deadline := time.Now().Add(*duration)

	var wg sync.WaitGroup
	for range *workers {
		wg.Go(func() {
			// ユーザー操作は同時には発生しないので、開始タイミングをずらす。
			time.Sleep(rand.N(500 * time.Millisecond))
			for time.Now().Before(deadline) {
				if requestPaymentWithRetry(*url, *maxRetries, *backoff) {
					success.Add(1)
				} else {
					giveup.Add(1)
				}
				time.Sleep(500 * time.Millisecond) // 次の決済リクエストまでの間隔。
			}
		})
	}
	wg.Wait()

	fmt.Printf("attempts=%d success=%d giveup=%d\n",
		attempts.Load(), success.Load(), giveup.Load())
}

func requestPaymentWithRetry(url string, maxRetries int, backoff bool) bool {
	next := baseBackoff // 次のバックオフの上限。
	for attempt := 0; ; attempt++ {
		ok, wait := requestPayment(url)
		if ok {
			return true
		}
		if attempt == maxRetries {
			return false // 最終試行の失敗後は待たずに諦める。
		}
		if backoff {
			// フルジッター: [0, next)の一様乱数を待ち時間とする。
			// サーバーがRetry-Afterで指示してきた場合はそちらを優先する。
			wait = max(wait, rand.N(next))
			next = min(next*2, maxBackoff) // 失敗のたびに上限を倍にする。
		}
		time.Sleep(wait) // Retry-Afterもバックオフもなければ即リトライ。
	}
}

// requestPayment は決済リクエストを1回送り、成功したかどうかと、
// サーバーがRetry-Afterで指示した待ち時間を返す。
func requestPayment(url string) (bool, time.Duration) {
	attempts.Add(1)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return false, 0
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) // 読み切らないと接続が再利用されない。
		resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusOK {
		return true, 0
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		return false, time.Duration(secs) * time.Second
	}
	return false, 0
}
