package vmess

import (
	"context"
	"net"

	"github.com/refraction-networking/utls"

	C "github.com/Dreamacro/clash/constant"
)

type TLSConfig struct {
	Host           string
	SkipCertVerify bool
	NextProtos     []string
}

func StreamTLSConn(conn net.Conn, cfg *TLSConfig) (net.Conn, error) {
	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.SkipCertVerify,
		NextProtos:         cfg.NextProtos,
	}

	tlsConn := tls.UClient(conn, tlsConfig, C.TLSClientHelloID)

	// fix tls handshake not timeout
	ctx, cancel := context.WithTimeout(context.Background(), C.DefaultTLSTimeout)
	defer cancel()
	err := tlsConn.HandshakeContext(ctx)
	return tlsConn, err
}
