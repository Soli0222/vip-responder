package main

import (
	"flag"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mdlayher/arp"
	"github.com/mdlayher/ethernet"
)

func main() {
	ifaceName := flag.String("iface", "eth0", "Interface to listen on")
	vipStr := flag.String("vip", "", "Comma-separated Virtual IP addresses (e.g. 10.0.0.1,10.0.0.2)")
	garpInterval := flag.Duration("garp-interval", 10*time.Second, "Interval for sending periodic GARP")
	flag.Parse()

	if *vipStr == "" {
		log.Fatal("VIP address is required")
	}

	// ---------------------------------------------------------
	// 1. 複数のIPをパースしてMapに格納する (検索高速化のため)
	// ---------------------------------------------------------
	// netip.Addr は比較可能なのでキーとして直接使用可能
	vipMap := make(map[netip.Addr]struct{})
	vipList := []netip.Addr{}

	rawVips := strings.Split(*vipStr, ",")
	for _, v := range rawVips {
		trimmed := strings.TrimSpace(v)
		parsedIP, err := netip.ParseAddr(trimmed)
		if err != nil {
			log.Fatalf("Invalid IP address: %s, error: %v", trimmed, err)
		}
		// IPv4のみ対応
		if !parsedIP.Is4() {
			log.Fatalf("Only IPv4 is supported: %s", trimmed)
		}

		vipMap[parsedIP] = struct{}{}
		vipList = append(vipList, parsedIP)
	}

	iface, err := net.InterfaceByName(*ifaceName)
	if err != nil {
		log.Fatalf("Could not get interface: %v", err)
	}

	c, err := arp.Dial(iface)
	if err != nil {
		log.Fatalf("Could not dial ARP: %v", err)
	}
	defer c.Close()

	log.Printf("Starting ARP responder on %s (%s)", iface.Name, iface.HardwareAddr)
	log.Printf("Monitoring VIPs: %v", rawVips)
	log.Printf("GARP interval: %v", *garpInterval)

	// ---------------------------------------------------------
	// 2. 起動時に担当する全IPのGARPを送信
	// ---------------------------------------------------------
	for _, vip := range vipList {
		if err := sendGARP(c, iface, vip); err != nil {
			log.Printf("Failed to send GARP for %s: %v", vip, err)
		} else {
			log.Printf("Sent GARP for %s", vip)
		}
	}

	// ---------------------------------------------------------
	// 3. 定期的にGARPを送出 (上流ルーターのARPキャッシュ更新)
	// ---------------------------------------------------------
	go func() {
		ticker := time.NewTicker(*garpInterval)
		defer ticker.Stop()
		for range ticker.C {
			for _, vip := range vipList {
				if err := sendGARP(c, iface, vip); err != nil {
					log.Printf("Failed to send periodic GARP for %s: %v", vip, err)
				}
			}
		}
	}()

	// ---------------------------------------------------------
	// 4. シグナルハンドリング
	// ---------------------------------------------------------
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("Shutting down...")
		c.Close()
		os.Exit(0)
	}()

	// ---------------------------------------------------------
	// 5. メインループ
	// ---------------------------------------------------------
	for {
		packet, _, err := c.Read()
		if err != nil {
			continue
		}

		if packet.Operation != arp.OperationRequest {
			continue
		}

		// ターゲットIPが「自分の担当リスト(Map)」にあるか確認
		// packet.TargetIP は netip.Addr 型
		if _, isMyVip := vipMap[packet.TargetIP]; !isMyVip {
			continue // 知らないIPなら無視
		}

		log.Printf("Received ARP request for %s. Replying...", packet.TargetIP)

		// 応答
		err = c.Reply(packet, iface.HardwareAddr, packet.TargetIP)
		if err != nil {
			log.Printf("Failed to reply: %v", err)
		}
	}
}

func sendGARP(c *arp.Client, iface *net.Interface, vip netip.Addr) error {
	packet, err := arp.NewPacket(
		arp.OperationReply,
		iface.HardwareAddr,
		vip,
		ethernet.Broadcast,
		vip,
	)
	if err != nil {
		return err
	}
	return c.WriteTo(packet, ethernet.Broadcast)
}
