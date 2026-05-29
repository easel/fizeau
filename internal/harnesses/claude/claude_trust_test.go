package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeTrustInterstitial_MatchesTrustDialogNotModelPicker(t *testing.T) {
	it := claudeTrustInterstitial()
	require.NotEmpty(t, it.Name)
	require.Equal(t, []byte("\r"), it.Send, "Enter accepts the pre-selected 'Yes, I trust this folder'")

	trustDialog := "Do you trust the files in this folder?\n" +
		"  No, exit\n" +
		"> Yes, I trust this folder ✔\n" +
		"Enter to confirm · Esc to cancel"
	require.True(t, it.Match(trustDialog), "must detect the folder-trust dialog")

	modelPicker := "Select model\n" +
		"1. Default (recommended) Opus 4.8 with 1M context\n" +
		"2. Sonnet  Sonnet 4.6\n" +
		"4. Haiku  Haiku 4.5"
	require.False(t, it.Match(modelPicker), "must NOT match the model picker output")

	require.False(t, it.Match("just some unrelated screen text"))
}
