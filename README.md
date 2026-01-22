# VIP Responder

指定されたVIP (Virtual IP) に対するARPリクエストに応答し、Gratuitous ARP (GARP) を定期的に送出するツールです。
L2ネットワークにおいて、特定のIPアドレスへのトラフィックをこのノードに引き寄せるために使用します。

## ビルド

```bash
go build -o vip-responder main.go
```

## 使い方

Root権限が必要です（Raw Socketを使用するため）。

```bash
sudo ./vip-responder \
  --iface eth0 \
  --vip 10.0.0.1,10.0.0.2 \
  --garp-interval 10s
```

### オプション

- `--vip`: **必須**。カンマ区切りのVIPリスト (例: `10.0.0.1,10.0.0.2`)
- `--iface`: リッスンするインターフェース (デフォルト: `eth0`)
- `--garp-interval`: GARPを送出する間隔 (デフォルト: `10s`)

---

## 運用パターン

このツールは主に以下の2つのパターンで運用できます。

### パターン1: Keepalived との連携 (推奨)

KeepalivedのMASTER/BACKUP状態に合わせて起動・停止します。
厳密なActive/Standby構成になります。

#### keepalived.conf 設定例

```nginx
vrrp_instance VI_1 {
    state MASTER
    interface eth0
    virtual_router_id 51
    priority 100
    advert_int 1
    
    # VIPはKeepalivedには管理させない（枯渇対策などでproxy_arpする場合）
    # virtual_ipaddress { ... } は書かない、または notifyスクリプトで制御
    
    notify_master "/path/to/vip-responder --iface eth0 --vip 10.0.0.1,10.0.0.2 & echo $! > /var/run/vip-responder.pid"
    notify_backup "kill $(cat /var/run/vip-responder.pid) && rm /var/run/vip-responder.pid"
    notify_fault "kill $(cat /var/run/vip-responder.pid) && rm /var/run/vip-responder.pid"
}
```

> **Note**: 本番運用では、`systemd`のワンショットサービスを定義して `systemctl start/stop` を叩く形にするとより管理しやすくなります。

### パターン2: 常時稼働 (Active-Active / Fast Failover)

全ノードで `vip-responder` を常時起動しておきます。
両方のノードがARPに応答しますが、受信側（上流ルーター）は通常、最初に到達したARP応答を採用します。

**メリット**: Keepalivedの設定がシンプル。フェイルオーバーが高速（切り替え待ちがない）。
**デメリット**: ネットワーク機器によってはARP Flappingとして検知される可能性があります（同一IPに対し異なるMACからの応答があるため）。

#### Systemd Service ファイル例 (/etc/systemd/system/vip-responder.service)

```ini
[Unit]
Description=VIP Responder
After=network.target

[Service]
ExecStart=/usr/local/bin/vip-responder --iface eth0 --vip 10.0.0.1,10.0.0.2 --garp-interval 10s
Restart=always
User=root

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now vip-responder
```
