package outbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/component/dialer"
	"github.com/Dreamacro/clash/component/proxydialer"
	tlsC "github.com/Dreamacro/clash/component/tls"
	C "github.com/Dreamacro/clash/constant"
)

type Http struct {
	*Base
	user      string
	pass      string
	tlsConfig *tls.Config
	option    *HttpOption
}

type HttpOption struct {
	BasicOption
	Name           string            `proxy:"name"`
	Server         string            `proxy:"server"`
	Port           int               `proxy:"port"`
	UserName       string            `proxy:"username,omitempty"`
	Password       string            `proxy:"password,omitempty"`
	TLS            bool              `proxy:"tls,omitempty"`
	SNI            string            `proxy:"sni,omitempty"`
	SkipCertVerify bool              `proxy:"skip-cert-verify,omitempty"`
	Fingerprint    string            `proxy:"fingerprint,omitempty"`
	Headers        map[string]string `proxy:"headers,omitempty"`
}

// StreamConn implements C.ProxyAdapter
func (h *Http) StreamConn(c net.Conn, metadata *C.Metadata) (net.Conn, error) {
	if h.tlsConfig != nil {
		cc := tls.Client(c, h.tlsConfig)
		ctx, cancel := context.WithTimeout(context.Background(), C.DefaultTLSTimeout)
		defer cancel()
		err := cc.HandshakeContext(ctx)
		c = cc
		if err != nil {
			return nil, fmt.Errorf("%s connect error: %w", h.addr, err)
		}
	}

	if err := h.shakeHand(metadata, c); err != nil {
		return nil, err
	}
	return c, nil
}

// DialContext implements C.ProxyAdapter
func (h *Http) DialContext(ctx context.Context, metadata *C.Metadata, opts ...dialer.Option) (_ C.Conn, err error) {
	return h.DialContextWithDialer(ctx, dialer.NewDialer(h.Base.DialOptions(opts...)...), metadata)
}

// DialContextWithDialer implements C.ProxyAdapter
func (h *Http) DialContextWithDialer(ctx context.Context, dialer C.Dialer, metadata *C.Metadata) (_ C.Conn, err error) {
	if len(h.option.DialerProxy) > 0 {
		dialer, err = proxydialer.NewByName(h.option.DialerProxy, dialer)
		if err != nil {
			return nil, err
		}
	}
	c, err := dialer.DialContext(ctx, "tcp", h.addr)
	if err != nil {
		return nil, fmt.Errorf("%s connect error: %w", h.addr, err)
	}
	tcpKeepAlive(c)

	defer func(c net.Conn) {
		safeConnClose(c, err)
	}(c)

	c, err = h.StreamConn(c, metadata)
	if err != nil {
		return nil, err
	}

	return NewConn(c, h), nil
}

// SupportWithDialer implements C.ProxyAdapter
func (h *Http) SupportWithDialer() C.NetWork {
	return C.TCP
}

func (h *Http) shakeHand(metadata *C.Metadata, rw io.ReadWriter) error {
	if _, ok := h.option.Headers["Host"]; ok {
		addr := metadata.RemoteAddress()
		header := "CONNECT " + addr + "HTTP/1.1\r\n"
		//增加headers
		if len(h.option.Headers) != 0 {
			for key, value := range h.option.Headers {
				header += key + ": " + value + "\r\n"
			}
		}
		if h.user != "" && h.pass != "" {
			auth := h.user + ":" + h.pass
			header += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(auth)) + "\r\n"
		}

		header += "\r\n"

		total, err := rw.Write([]byte(header))

		if err != nil {
			log.Errorln("connect "+addr, err)
			return nil
		}
		rd := make([]byte, total)

		if _, err := rw.Read(rd); err == nil {
			line := strings.Split(string(rd), "\n")[0]
			httpStatus := strings.Split(line, " ")[1]
			switch httpStatus {
			case "200":
				return nil
			case "407":
				return errors.New("HTTP need auth")
			case "405":
				return errors.New("CONNECT method not allowed by proxy")
			default:
				return errors.New(string(rd))
			}
		} else {
			return nil
		}

		return fmt.Errorf("can not connect remote err code: %s", string(rd))

	} else {
		// Host header does not exist
		addr := metadata.RemoteAddress()
		req := &http.Request{
			Method: http.MethodConnect,
			URL: &url.URL{
				Host: addr,
			},
			Host: addr,
			Header: http.Header{
				"Proxy-Connection": []string{"Keep-Alive"},
			},
		}

		//增加headers
		if len(h.option.Headers) != 0 {
			for key, value := range h.option.Headers {
				req.Header.Add(key, value)
			}
		}

		if h.user != "" && h.pass != "" {
			auth := h.user + ":" + h.pass
			req.Header.Add("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
		}

		if err := req.Write(rw); err != nil {
			return err
		}

		resp, err := http.ReadResponse(bufio.NewReader(rw), req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusOK {
			return nil
		}

		if resp.StatusCode == http.StatusProxyAuthRequired {
			return errors.New("HTTP need auth")
		}

		if resp.StatusCode == http.StatusMethodNotAllowed {
			return errors.New("CONNECT method not allowed by proxy")
		}

		if resp.StatusCode >= http.StatusInternalServerError {
			return errors.New(resp.Status)
		}

		return fmt.Errorf("can not connect remote err code: %d", resp.StatusCode)

	}
}

func NewHttp(option HttpOption) (*Http, error) {
	var tlsConfig *tls.Config
	if option.TLS {
		sni := option.Server
		if option.SNI != "" {
			sni = option.SNI
		}
		if len(option.Fingerprint) == 0 {
			tlsConfig = tlsC.GetGlobalTLSConfig(&tls.Config{
				InsecureSkipVerify: option.SkipCertVerify,
				ServerName:         sni,
			})
		} else {
			var err error
			if tlsConfig, err = tlsC.GetSpecifiedFingerprintTLSConfig(&tls.Config{
				InsecureSkipVerify: option.SkipCertVerify,
				ServerName:         sni,
			}, option.Fingerprint); err != nil {
				return nil, err
			}
		}
	}

	return &Http{
		Base: &Base{
			name:   option.Name,
			addr:   net.JoinHostPort(option.Server, strconv.Itoa(option.Port)),
			tp:     C.Http,
			tfo:    option.TFO,
			iface:  option.Interface,
			rmark:  option.RoutingMark,
			prefer: C.NewDNSPrefer(option.IPVersion),
		},
		user:      option.UserName,
		pass:      option.Password,
		tlsConfig: tlsConfig,
		option:    &option,
	}, nil
}
