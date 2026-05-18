package gemini

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGeminiModels(t *testing.T) {
	models := parseGeminiModels("Models\r\n> gemini-2.5-pro\r\n  gemini-2.5-flash\r\n  gemini-2.5-pro\r\n")
	require.Equal(t, []string{"gemini-2.5-pro", "gemini-2.5-flash"}, models)
}

func TestResolveGeminiModelAliasUsesLatestDiscoveredVersion(t *testing.T) {
	snapshot := testGeminiModelDiscovery()
	snapshot.Models = []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3.0-flash"}

	require.Equal(t, "gemini-3.0-flash", resolveGeminiModelAlias("gemini", snapshot))
	require.Equal(t, "gemini-2.5-pro", resolveGeminiModelAlias("gemini-2.5", snapshot))
	require.Equal(t, "gemini-2.5-flash", resolveGeminiModelAlias("gemini-2.5-flash", snapshot))
	require.Equal(t, "qwen3.6", resolveGeminiModelAlias("qwen3.6", snapshot))
}
