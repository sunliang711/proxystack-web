package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/subscription"
	"proxystack-web/internal/subserver"
)

func TestSubSourcePublishTokenCommands(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	layout := subserver.Layout{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(layout.Dir, 0o750))
	config := subserver.DefaultConfig()
	config.Sources = []subserver.SourceConfig{{
		Name: "main", PublicKey: subscription.FormatPublicKey(key.Public().(ed25519.PublicKey)), Primary: true,
	}}
	require.NoError(t, subserver.SaveConfig(layout, config))

	token := "psw_pub_01234567890123456789012345678901"
	command := newSubSourceCommand(func() subserver.Layout { return layout })
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	command.SetIn(strings.NewReader(token + "\n"))
	command.SetArgs([]string{"set-publish-token", "main"})
	require.NoError(t, command.Execute())
	require.NotContains(t, output.String(), token)

	config, err = subserver.LoadConfig(layout)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(token))
	require.Equal(t, hex.EncodeToString(sum[:]), config.Sources[0].PublishTokenHash)

	command = newSubSourceCommand(func() subserver.Layout { return layout })
	command.SetOut(output)
	command.SetArgs([]string{"clear-publish-token", "main"})
	require.NoError(t, command.Execute())
	config, err = subserver.LoadConfig(layout)
	require.NoError(t, err)
	require.Empty(t, config.Sources[0].PublishTokenHash)
}
