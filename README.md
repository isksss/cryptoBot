# 仮想通貨売買bot

GMOコインの現物取引を対象に、定期的に価格取得と注文発行を行う売買botを 0 から構築する。
まずは個人運用を前提とした小規模な自動売買システムとして実装し、安全性と監査性を優先する。

## 目的

- BTC, ETH の現物売買を自動化する
- 完全放置ではなく、ルールベースで淡々と注文を出す
- 注文、約定、価格推移を永続化し、後から検証できるようにする
- 自宅サーバー上で docker compose により常時稼働させる

## 前提

- 取引所は GMOコイン
- 対象は現物取引のみ
- まずは BTC, ETH を対象とする
- 稼働環境は自宅サーバー上の Linux + Docker Compose
- アプリケーションは常時稼働
- bot と管理 API は同一バイナリで稼働する
- 保存先DBは PostgreSQL
- 時刻の基準は JST 固定とする

## 設定値

- `CRYPTOBOT_TARGETS`: 売買対象通貨。初期値は `BTC,ETH`
- `CRYPTOBOT_WEEKLY_LIMIT_UNITS`: 直近7日間の新規買い上限数量
- `CRYPTOBOT_API_KEY`: GMOコイン API キー
- `CRYPTOBOT_API_SECRET_KEY`: GMOコイン API シークレットキー
- `CRYPTOBOT_ADMIN_USERNAME`: 管理 API / 管理画面に入る Basic 認証ユーザー名
- `CRYPTOBOT_ADMIN_PASSWORD`: 管理 API / 管理画面に入る Basic 認証パスワード
- `CRYPTOBOT_DRY_RUN`: 注文を実発行せず、判定と記録だけ行うフラグ
- `CRYPTOBOT_PRICE_SYNC_INTERVAL`: 価格同期の定期実行間隔。初期値は `1h`
- `CRYPTOBOT_ORDER_RECONCILE_INTERVAL`: 注文状態同期の定期実行間隔。初期値は `5m`
- `CRYPTOBOT_DATABASE_URL`: PostgreSQL の接続先
- `CRYPTOBOT_HTTP_ADDR`: 管理 API の待受アドレス。初期値は `:8080`
- `CRYPTOBOT_LOG_LEVEL`: ログレベル

## アクセス制御

- `/api/v1/*`, `/`, `/ui/*` は Basic 認証で保護する
- 認証には `CRYPTOBOT_ADMIN_USERNAME` と `CRYPTOBOT_ADMIN_PASSWORD` を使う
- `/healthz` はコンテナや外形監視向けに無認証で公開する
- `htmx` の手動操作は管理画面と同じ認証コンテキストで実行されるため、未認証では実行できない

API 呼び出し例:

```bash
curl -u "$CRYPTOBOT_ADMIN_USERNAME:$CRYPTOBOT_ADMIN_PASSWORD" \
  http://localhost:8080/api/v1/system/summary
```

## 基本動作

- 1時間ごとに BTC, ETH の価格を取得して保存する
- 数分おきに GMO へ注文状態同期を行い、部分約定や取消を DB に反映する
- 毎日 0:00 JST に売買判定を行う
- 判定時点の保有資産、未約定注文、直近7日間の新規買い数量を取得した上で注文を決定する
- 発行した注文とその後の約定状態を追跡して保存する
- 前日に発行して未解消の注文は、翌日の売買判定前に約定確認を行い、必要に応じてキャンセルまで含めた状態遷移として解消する
- 現在価格は、対象通貨 1 単位あたりの日本円価格として扱う
- 起動時に GMO コインから最新の保有残高と BTC, ETH の現在価格を取得し、DB に初回スナップショットを保存する

## 初期売買ロジック

- 戦略計算には手数料を含める
- 注文は、手数料が発生しないポジションで約定させる前提で価格と数量を決定する
- 売り注文
  - 対象通貨の保有数量の 5% を、判定時の現在価格の 105% で指値売りする
- 買い注文
  - 保有している日本円の 50% 分を上限として、判定時の現在価格の 90% で指値買いする
- 上記はベースライン戦略であり、将来的に戦略差し替え可能な設計にする

## 注文時の安全装置

- `CRYPTOBOT_WEEKLY_LIMIT_UNITS` を超える新規買いは行わない
- 最小注文数量、最小注文金額、価格刻みは取引所仕様に従って補正する
- 未約定注文が残っている場合は、重複発注しない
- 前日注文が解消されていない場合は、その日の新規注文を起票しない
- 注文前に利用可能残高を再確認する
- API エラー、通信失敗、署名エラー時はリトライ回数を制限し、無限再試行しない
- 一定回数以上の連続失敗時は新規注文を停止し、異常状態として扱う
- 起動直後に同日の注文済みかを確認し、二重実行を防ぐ
- 直近7日間の新規買い数量を通貨ごとに集計し、ローリングウィンドウで上限判定する

## 監視・運用要件

- 重要イベントを構造化ログとして出力する
  - 価格取得
  - 売買判定
  - 注文送信
  - 約定確認
  - エラー
- ヘルスチェック用の仕組みを持つ
- dry-run モードでローカル検証可能にする
- 再起動後も DB を用いて状態復元できるようにする
- 将来的に通知機能を追加できる構成にするが、初期実装には含めない
- 管理画面は Go template と htmx で同一バイナリに同居させる

## API 方針

- API は OpenAPI を正とする
- OpenAPI 定義から Go の型とサーバーインターフェースを生成する
- API と管理画面は bot と同一バイナリで公開する
- `/api/v1/*`, `/`, `/ui/*` は Basic 認証で保護し、`/healthz` は無認証で公開する
- SQL は `sqlc` を使って Go のクエリコードを生成する
- OpenAPI 定義は [api/openapi.yaml](/home/isksss/ghq/github.com/isksss/cryptoBot/api/openapi.yaml) に置く
- 生成設定は [api/oapi-codegen.yaml](/home/isksss/ghq/github.com/isksss/cryptoBot/api/oapi-codegen.yaml) で管理する
- 生成コードの更新は `go generate ./internal/api` で行う
- `sqlc` の生成コード更新は `go generate ./internal/store` で行う
- ローカル開発用の環境変数サンプルは [.env.example](/home/isksss/ghq/github.com/isksss/cryptoBot/.env.example) を参照する

## テスト

- 単体テストは `go test ./...` で実行する
- [internal/sync/service_integration_test.go](/home/isksss/ghq/github.com/isksss/cryptoBot/internal/sync/service_integration_test.go) は PostgreSQL が必要
- `docker compose up -d postgres` で DB を起動してから実行すると、統合テストまで含めて確認できる

## データ保存

PostgreSQL に少なくとも以下を保存する。

- 価格履歴
  - 通貨
  - 取得時刻
  - 価格
- 注文履歴
  - 注文ID
  - 通貨
  - 売買区分
  - 注文種別
  - 価格
  - 数量
  - 注文時刻
  - 注文ステータス
  - 失効時刻またはキャンセル時刻
  - 最終状態確認時刻
- 約定履歴
  - 約定ID
  - 対象注文ID
  - 約定価格
  - 約定数量
  - 約定時刻
  - 部分約定フラグ
- 実行履歴
  - ジョブ種別
  - 実行時刻
  - 成否
  - エラー内容

DB の詳細設計は [docs/db-design.md](/home/isksss/ghq/github.com/isksss/cryptoBot/docs/db-design.md) を参照する。
初期スキーマは [db/init/001_schema.sql](/home/isksss/ghq/github.com/isksss/cryptoBot/db/init/001_schema.sql) で管理する。

## 実装上の懸念点

- 前日注文の解消失敗が継続すると、新規注文が長期間停止する可能性がある
- 約定確認とキャンセル処理の間に状態が変化する競合を考慮する必要がある
- 買い注文に「保有円の 50%」を使うと、既存の未約定買い注文と合算して過剰発注になる可能性がある
- 売り注文に「保有数の 5%」を使うと、既存の未約定売り注文を考慮しない場合に二重拘束が起きる
- 手数料無料となる注文条件が取引所仕様変更で変わる可能性がある
- API のレート制限やメンテナンス時間を考慮する必要がある
- PostgreSQL の接続断時に注文状態同期が遅延する可能性がある
- 自宅サーバー運用では停電、ネットワーク断、時刻ずれへの対策が必要

## MVP の範囲

- GMOコインの認証付き API クライアント実装
- 価格取得ジョブ
- 日次売買判定ジョブ
- 前日注文の解消ジョブ
- 注文発行、注文状態同期
- PostgreSQL への永続化
- dry-run モード
- 管理用 API
- Go template + htmx の管理画面
- Docker Compose での起動

## 今後の拡張候補

- バックテスト
- フロントエンド UI
- 通知連携
- 戦略のプラグイン化
- 複数取引所対応
