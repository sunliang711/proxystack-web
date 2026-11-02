package subpublish

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestProbeSourceHeaderMustMatchTarget(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: http.NoBody}
	response.Header.Set("X-Proxystack-Source", "other")
	require.EqualError(t, validatePublishProbeResponse(response, Target{Source: "main"}), "publish Secret is bound to a different source")

	response.Header.Set("X-Proxystack-Source", "main")
	require.NoError(t, validatePublishProbeResponse(response, Target{Source: "main"}))
}

func TestRemoteAddressAlwaysUsesLoopback(t *testing.T) {
	require.Equal(t, "127.0.0.1:9444", remoteAddress(Target{SSHHost: "example.com", RemotePort: 9444}))
}

func TestHostKeyAlgorithmsMatchConfiguredKeyType(t *testing.T) {
	require.Equal(t, []string{ssh.KeyAlgoED25519}, hostKeyAlgorithms[ssh.KeyAlgoED25519])
	require.Equal(t, []string{ssh.KeyAlgoECDSA256}, hostKeyAlgorithms[ssh.KeyAlgoECDSA256])
	require.Equal(t, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}, hostKeyAlgorithms[ssh.KeyAlgoRSA])
	require.True(t, SupportedHostKeyType(ssh.KeyAlgoRSA))
	require.False(t, SupportedHostKeyType("ssh-dss"))
}

func TestConnectNegotiatesConfiguredHostKeyType(t *testing.T) {
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	clientPublic, err := ssh.NewPublicKey(clientPrivate.Public())
	require.NoError(t, err)
	_, ed25519HostPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ed25519Host, err := ssh.NewSignerFromKey(ed25519HostPrivate)
	require.NoError(t, err)
	ecdsaHostPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecdsaHost, err := ssh.NewSignerFromKey(ecdsaHostPrivate)
	require.NoError(t, err)

	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), clientPublic.Marshal()) {
				return nil, errors.New("unexpected client key")
			}
			return nil, nil
		},
	}
	serverConfig.AddHostKey(ed25519Host)
	serverConfig.AddHostKey(ecdsaHost)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	handshake := make(chan error, 1)
	go func() {
		raw, acceptErr := listener.Accept()
		if acceptErr != nil {
			handshake <- acceptErr
			return
		}
		connection, channels, requests, serverErr := ssh.NewServerConn(raw, serverConfig)
		handshake <- serverErr
		if serverErr != nil {
			_ = raw.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			_ = channel.Reject(ssh.Prohibited, "channels are disabled in this test")
		}
		_ = connection.Close()
	}()

	address := listener.Addr().(*net.TCPAddr)
	stages := []Stage{}
	connection, err := connect(context.Background(), Identity{Seed: clientPrivate.Seed()}, Target{
		SSHHost: "127.0.0.1", SSHPort: address.Port, SSHUser: "publisher",
		HostKeyAlgorithm: ssh.KeyAlgoECDSA256, HostKeyFingerprint: ssh.FingerprintSHA256(ecdsaHost.PublicKey()),
	}, &stages)
	require.NoError(t, err)
	require.NoError(t, <-handshake)
	connection.Close()
	require.Equal(t, "succeeded", stages[1].Status)
}

func TestHTTPClientRejectsRedirects(t *testing.T) {
	client := targetHTTPClient(nil, Target{RemotePort: 9444}, probeTimeout)
	err := client.CheckRedirect(&http.Request{}, []*http.Request{{}})
	require.EqualError(t, err, "HTTP redirects are not allowed")
}

func TestReadJSONResponseRejectsErrorsAndOversizedBodies(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader(`{"error":"secret"}`)),
	}
	require.EqualError(t, readJSONResponse(response, nil), "subscription server returned HTTP 401")

	response = &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))),
	}
	require.EqualError(t, readJSONResponse(response, nil), "subscription server response is too large")
}
