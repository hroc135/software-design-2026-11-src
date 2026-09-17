# software-design-2026-11-src

Software Design 2026年11月号第3章のサンプルコード。

## 必要なもの

* Go 1.26以上
* Python 3 + matplotlib（サーバーログのグラフ化）

## step1〜4

クライアントは全ステップ共通で、起動するサーバーとクライアントのフラグだけを変えて挙動を比較する。ターミナルを2つ使う。

ターミナル1: サーバーを起動する

| step | サーバー | クライアント | 挙動 | 図 |
| --- | --- | --- | --- | --- |
| 1 | `go run ./server/step1` | `go run ./client -duration 20s` | 503にRetry-Afterを付けない(クライアントは即リトライ) | 図1 |
| 2 | `go run ./server/step2-retry-after` | 同上 | 503に固定値 `Retry-After: 3` を付ける | 図2 |
| 3 | `go run ./server/step3-jitter` | 同上 | Retry-Afterの3秒に±1秒のジッターを加える | 図3 |
| 4 | `go run ./server/step1` | `go run ./client -duration 20s -backoff` | サーバーはstep1のまま、クライアントが指数バックオフで自衛する | 図4 |


ターミナル2: クライアントを実行し、途中で障害を注入する

```console
$ go run ./client -duration 20s &                # 20秒間、60ワーカーが決済リクエストを送る
$ sleep 5 && curl 'localhost:8080/outage?d=5s'   # 開始から5秒後に、5秒間の障害を注入する
```

`-backoff` を付けた場合は、待ち時間を指数バックオフとフルジッターで自分で決める。

主なフラグ: サーバー `-addr` `-capacity` `-latency`、クライアント `-url` `-workers` `-duration` `-max-retries` `-backoff`

### ログのグラフ化

`logs/` に保存したサーバーログを Python 3 + matplotlib で描画する（`figures/` に出力）。

```console
$ python3 scripts/plot_server_log.py logs/step1.log figures/fig1.png --ymax 300
$ python3 scripts/plot_server_log.py logs/step2.log figures/fig2.png --ymax 300
$ python3 scripts/plot_server_log.py logs/step3.log figures/fig3.png --ymax 300
$ python3 scripts/plot_server_log.py logs/step4-backoff.log figures/fig4.png --ymax 300
```

### idempotency: exactly onceの実現

ターミナル1: 決済サーバーを起動する

```console
$ go run ./idempotency/server
payment server listening on :8081 (drop-rate=30%)
```

ターミナル2: クライアントをキーなし・キーありの順で実行する

```console
$ go run ./idempotency/client -payments 10           # Idempotency-Keyなし
$ curl -X POST localhost:8081/reset                  # サーバーの状態をリセットする
$ go run ./idempotency/client -payments 10 -use-key  # Idempotency-Keyあり
```

確定した決済の一覧は `curl localhost:8081/payments` でも確認できる。

`-use-key` を付けると、決済ごとに一意なIdempotency-Keyを生成し、リトライでは同じキーを使い回す。
サーバーはキーごとに処理結果を保存し、同じキーの再送には保存済みの結果を返す（`Idempotency-Replayed: true`）。
処理中に同じキーが届いた場合は完了を待って同じ結果を返すため、並行な重複にも安全。

主なフラグ: サーバー `-addr` `-drop-rate`、クライアント `-base` `-payments` `-amount` `-use-key`
