# DB設計

## 方針

- DB は PostgreSQL を使用する
- 監査性を優先し、価格取得、残高取得、注文、約定、ジョブ実行を分けて保存する
- 注文の最新状態だけでなく、状態遷移もイベントとして保持する
- 時刻はすべて `timestamptz` で保存し、アプリケーション上の業務時刻は JST 固定で扱う
- 数量、価格、手数料は誤差回避のため `numeric` を使う

## テーブル一覧

- `price_snapshots`
  - 通貨ごとの価格履歴
- `balance_snapshots`
  - 判定時点や同期時点の資産残高履歴
- `job_runs`
  - 定期ジョブの実行履歴
- `orders`
  - 注文の最新状態
- `order_events`
  - 注文の状態遷移履歴
- `trade_executions`
  - 約定履歴。部分約定も 1 レコードずつ保持する

## price_snapshots

価格取得ジョブの結果を保存する。

| カラム | 型 | 説明 |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `asset_code` | `text` | `BTC` などの通貨コード |
| `price_jpy` | `numeric(20,8)` | 1単位あたりの日本円価格 |
| `captured_at` | `timestamptz` | 価格取得時刻 |
| `source` | `text` | 価格ソース識別子 |
| `created_at` | `timestamptz` | 作成時刻 |

主な用途:

- 時系列分析
- 売買判定時の参照価格
- 後日の検証

## balance_snapshots

残高取得結果を保存する。注文判定前の監査証跡として使う。

| カラム | 型 | 説明 |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `job_run_id` | `bigint` | `job_runs.id` への FK。手動同期時は NULL 可 |
| `asset_code` | `text` | `JPY`, `BTC`, `ETH` |
| `available_amount` | `numeric(20,8)` | 利用可能残高 |
| `locked_amount` | `numeric(20,8)` | 拘束中残高 |
| `captured_at` | `timestamptz` | 残高取得時刻 |
| `created_at` | `timestamptz` | 作成時刻 |

主な用途:

- 注文前の原資確認
- 前日注文未解消時の調査
- 運用監査

## job_runs

定期ジョブや手動実行の結果を保存する。

| カラム | 型 | 説明 |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `job_type` | `text` | `price_fetch`, `daily_trade`, `order_reconcile` など |
| `status` | `text` | `running`, `succeeded`, `failed`, `skipped` |
| `scheduled_for` | `timestamptz` | 本来の実行予定時刻 |
| `started_at` | `timestamptz` | 実行開始時刻 |
| `finished_at` | `timestamptz` | 実行終了時刻 |
| `error_code` | `text` | エラー識別子 |
| `error_message` | `text` | エラー詳細 |
| `metadata` | `jsonb` | 判定条件や集計結果 |
| `created_at` | `timestamptz` | 作成時刻 |

主な用途:

- 失敗追跡
- 重複実行防止
- API の監査用ログ保存

## orders

注文の最新状態を保持する。GMOコイン上の注文 1 件につき 1 レコード。

| カラム | 型 | 説明 |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `job_run_id` | `bigint` | 発行元の `job_runs.id` |
| `exchange_order_id` | `text` | 取引所注文ID。一意制約あり |
| `client_order_id` | `uuid` | アプリ側の冪等キー。一意制約あり |
| `asset_code` | `text` | `BTC`, `ETH` |
| `side` | `text` | `buy` または `sell` |
| `order_type` | `text` | MVP では `limit` 固定 |
| `status` | `text` | `pending_submit`, `open`, `partially_filled`, `filled`, `cancel_requested`, `cancelled`, `expired`, `rejected`, `failed` |
| `price_jpy` | `numeric(20,8)` | 注文価格 |
| `ordered_units` | `numeric(20,8)` | 注文数量 |
| `filled_units` | `numeric(20,8)` | 累計約定数量 |
| `remaining_units` | `numeric(20,8)` | 残数量 |
| `fee_jpy` | `numeric(20,8)` | 累計手数料 |
| `is_fee_free` | `boolean` | 手数料無料条件を満たす前提で起票したか |
| `placed_at` | `timestamptz` | 注文送信時刻 |
| `expires_at` | `timestamptz` | 翌日解消対象となる失効時刻 |
| `cancelled_at` | `timestamptz` | キャンセル完了時刻 |
| `last_status_checked_at` | `timestamptz` | 最終状態確認時刻 |
| `created_at` | `timestamptz` | 作成時刻 |
| `updated_at` | `timestamptz` | 更新時刻 |

制約方針:

- `filled_units <= ordered_units`
- `remaining_units = ordered_units - filled_units` をアプリケーションで維持
- `exchange_order_id` と `client_order_id` に一意制約

主な用途:

- 現在の未約定注文判定
- 前日注文の解消確認
- 直近7日間の新規買い数量集計

## order_events

注文の状態遷移をイベントとして保存する。監査証跡はこのテーブルが正本になる。

| カラム | 型 | 説明 |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `order_id` | `bigint` | `orders.id` への FK |
| `job_run_id` | `bigint` | 遷移を発生させた `job_runs.id` |
| `event_type` | `text` | `submitted`, `opened`, `partial_fill`, `filled`, `cancel_requested`, `cancelled`, `expired`, `rejected`, `sync_failed` |
| `from_status` | `text` | 遷移前状態 |
| `to_status` | `text` | 遷移後状態 |
| `event_at` | `timestamptz` | 取引所または同期上の発生時刻 |
| `payload` | `jsonb` | API レスポンスや補足情報 |
| `created_at` | `timestamptz` | 作成時刻 |

主な用途:

- 約定確認を含む状態遷移の記録
- 事故調査
- API レスポンスの追跡

## trade_executions

約定履歴を保存する。部分約定のたびにレコードを追加する。

| カラム | 型 | 説明 |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `order_id` | `bigint` | `orders.id` への FK |
| `exchange_execution_id` | `text` | 取引所約定ID。一意制約あり |
| `executed_at` | `timestamptz` | 約定時刻 |
| `price_jpy` | `numeric(20,8)` | 約定価格 |
| `executed_units` | `numeric(20,8)` | 約定数量 |
| `fee_jpy` | `numeric(20,8)` | 約定手数料 |
| `is_partial_fill` | `boolean` | 部分約定なら `true` |
| `created_at` | `timestamptz` | 作成時刻 |

主な用途:

- 部分約定の記録
- 取得手数料込みの損益計算
- 注文ステータス同期

## インデックス方針

- `price_snapshots (asset_code, captured_at desc)`
- `balance_snapshots (asset_code, captured_at desc)`
- `job_runs (job_type, scheduled_for desc)`
- `orders (asset_code, status, placed_at desc)`
- `orders (side, status, placed_at desc)`
- `trade_executions (order_id, executed_at asc)`
- `order_events (order_id, event_at asc)`

## 直近7日間の新規買い数量の考え方

- 集計対象は `orders.side = 'buy'` の注文
- `pending_submit`, `failed`, `rejected` は集計対象外
- `open`, `partially_filled`, `filled`, `cancelled`, `expired` は「実際に市場へ出した数量」として `ordered_units` を集計対象にする
- 集計基準時刻は `placed_at`
- 期間条件は `placed_at >= now() - interval '7 days'`

この仕様にすると、発注後すぐキャンセルされた注文も上限消費として扱う。意図的な連打を抑制できる一方で、厳しめの制限になる。

## 前日注文解消フローで使う状態遷移

1. `open` または `partially_filled` の注文を取得する
2. 取引所APIで最新状態を確認する
3. 約定済みなら `filled` または `partially_filled` に更新する
4. 未約定残があれば `cancel_requested` を経由して `cancelled` へ更新する
5. 1件でも未解消なら当日の新規注文は起票しない

## 備考

- 将来 API を生やす前提で、主キーはすべて外部公開しやすい単純な surrogate key を使う
- 列挙型は PostgreSQL の `enum` ではなく `text + check 制約` で始める
- マイグレーションツール導入後は `db/init/001_schema.sql` を初期投入用として固定し、以降は別 migration に切り出す
- 管理 API / 管理画面の Basic 認証情報は DB には保存せず、環境変数で管理する
