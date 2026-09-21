package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeTrustInterstitial_MatchesTrustDialogNotModelPicker(t *testing.T) {
	it := claudeTrustInterstitial()
	require.NotEmpty(t, it.Name)
	require.NotNil(t, it.Respond)

	legacy := "Do you trust the files in this folder?\n" +
		"  No, exit\n" +
		"> Yes, I trust this folder ✔\n" +
		"Enter to confirm · Esc to cancel"
	require.True(t, it.Match(legacy), "must detect the folder-trust dialog")
	send, done := it.Respond(legacy)
	require.Equal(t, []byte("\r"), send, "Enter confirms when Yes is already highlighted")
	require.False(t, done, "ready detection waits until the dialog leaves the screen")

	currentDialog := claudeTrustInterstitial()
	current := "Quick safety check: Is this a project you created or one you trust?\n" +
		"❯ No, exit\n" +
		"  Yes, I trust this folder\n" +
		"Enter to confirm · Esc to cancel"
	require.True(t, currentDialog.Match(current), "must detect the current default-No dialog")
	send, done = currentDialog.Respond(current)
	require.Equal(t, []byte("\x1b[B"), send, "Down moves off the default No, exit row")
	require.False(t, done)

	modelPicker := "Select model\n" +
		"1. Default (recommended) Opus 4.8 with 1M context\n" +
		"2. Sonnet  Sonnet 4.6\n" +
		"4. Haiku  Haiku 4.5"
	require.False(t, it.Match(modelPicker), "must NOT match the model picker output")

	require.False(t, it.Match("just some unrelated screen text"))
}
