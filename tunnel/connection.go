package tunnel

import (
	"errors"
	"net"
	"net/netip"
	"time"

	N "github.com/Dreamacro/clash/common/net"
	"github.com/Dreamacro/clash/common/pool"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"

	M "github.com/sagernet/sing/common/metadata"
)

func handleUDPToRemote(packet C.UDPPacket, pc C.PacketConn, metadata *C.Metadata) error {
	addr := metadata.UDPAddr()
	if addr == nil {
		return errors.New("udp addr invalid")
	}

	if _, err := pc.WriteTo(packet.Data(), addr); err != nil {
		return err
	}
	// reset timeout
	_ = pc.SetReadDeadline(time.Now().Add(udpTimeout))

	return nil
}

func handleUDPToLocal(packet C.UDPPacket, pc net.PacketConn, key string, oAddr, fAddr netip.Addr) {
	buf := pool.Get(pool.UDPBufferSize)
	defer func() {
		_ = pc.Close()
		closeAllLocalCoon(key)
		natTable.Delete(key)
		_ = pool.Put(buf)
	}()

	for {
		_ = pc.SetReadDeadline(time.Now().Add(udpTimeout))
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		if fromUDPAddr, ok := from.(*net.UDPAddr); ok {
			_fromUDPAddr := *fromUDPAddr
			fromUDPAddr = &_fromUDPAddr // make a copy
			fromAddr := fromUDPAddr.AddrPort().Addr().Unmap()
			if fAddr.IsValid() && (oAddr.Unmap() == fromAddr) {
				fromUDPAddr.IP = fAddr.Unmap().AsSlice()
			} else {
				fromUDPAddr.IP = fromAddr.AsSlice()
			}
			from = fromUDPAddr
		} else if fromSockAddr, ok := from.(M.Socksaddr); ok {
			fromSockAddr = fromSockAddr.Unwrap()
			if fAddr.IsValid() && (oAddr.Unmap() == fromSockAddr.Addr) {
				fromSockAddr.Addr = fAddr.Unmap()
			}
			from = fromSockAddr
		}

		_, err = packet.WriteBack(buf[:n], from)
		if err != nil {
			return
		}
	}
}

func closeAllLocalCoon(lAddr string) {
	natTable.RangeLocalConn(lAddr, func(key, value any) bool {
		conn, ok := value.(*net.UDPConn)
		if !ok || conn == nil {
			log.Debugln("Value %#v unknown value when closing TProxy local conn...", conn)
			return true
		}
		conn.Close()
		log.Debugln("Closing TProxy local conn... lAddr=%s rAddr=%s", lAddr, key)
		return true
	})
}

func handleSocket(ctx C.ConnContext, outbound net.Conn) {
	left := unwrap(ctx.Conn())
	right := unwrap(outbound)

	if relayHijack(left, right) {
		return
	}
	N.Relay(ctx.Conn(), outbound)
}
