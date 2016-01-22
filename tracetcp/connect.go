package tracetcp

import (
	//"fmt"
	"net"
	"syscall"
	"time"
)

type implTraceEventType int

const (
	beginConnect implTraceEventType = iota
	timedOut
	ttlExpired
	connected
	connectFailed
	errored
)

type implTraceEvent struct {
	evtype    implTraceEventType
	timeStamp time.Time

	localPort  int
	remotePort int
	remoteAddr net.IPAddr
	localAddr  net.IPAddr
	ttl        int
	query      int
	err        error
}

func makeErrorEvent(event *implTraceEvent, err error) implTraceEvent {
	event.err = err
	event.evtype = errored
	event.timeStamp = time.Now()
	return *event
}

func makeEvent(event *implTraceEvent, evtype implTraceEventType) implTraceEvent {
	event.evtype = evtype
	event.timeStamp = time.Now()
	return *event
}

func tryConnect(dest net.IPAddr, port, ttl, query int,
	timeout time.Duration, result chan implTraceEvent) {

	// fill in the event with as much info as we have so far
	event := implTraceEvent{
		remoteAddr: dest,
		remotePort: port,
		ttl:        ttl,
		query:      query,
	}

	sock, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}
	defer syscall.Close(sock)

	err = syscall.SetsockoptInt(sock, 0x0, syscall.IP_TTL, ttl)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	err = syscall.SetNonblock(sock, true)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	// ignore error from connect in non-blocking mode. as it will always return an
	// in progress error
	_ = syscall.Connect(sock, ToSockaddrInet4(dest, port))

	// get the local ip address and port number
	local, err := syscall.Getsockname(sock)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	// fill in the local endpoint deatils on the event struct
	event.localAddr, event.localPort, err = ToIPAddrAndPort(local)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	result <- makeEvent(&event, beginConnect)

	fdset := &syscall.FdSet{}
	timeoutVal := MakeTimeval(timeout)

	FD_ZERO(fdset)
	FD_SET(fdset, sock)

	_, err = syscall.Select(sock+1, nil, fdset, nil, &timeoutVal)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	// TODO: test for connect failed?

	if FD_ISSET(fdset, sock) {
		// detect if actually connected as select shows ttl expired as connected
		// so if we try to get the remote address and it fails then ttl has expired
		_, err = syscall.Getpeername(sock)
		if err == nil {
			result <- makeEvent(&event, connected)
		} else {
			result <- makeEvent(&event, connectFailed)
		}
	} else {
		result <- makeEvent(&event, timedOut)
	}
}

func receiveICMP(result chan implTraceEvent) {
	event := implTraceEvent{}

	// Set up the socket to receive inbound packets
	sock, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	err = syscall.Bind(sock, &syscall.SockaddrInet4{})
	if err != nil {
		result <- makeErrorEvent(&event, err)
		return
	}

	var pkt = make([]byte, 1024)
	for {
		_, from, err := syscall.Recvfrom(sock, pkt, 0)
		if err != nil {
			result <- makeErrorEvent(&event, err)
			return
		}

		// fill in the local endpoint deatils on the event struct
		event.localAddr, _, _ = ToIPAddrAndPort(from)
		result <- makeEvent(&event, ttlExpired)
	}
}
