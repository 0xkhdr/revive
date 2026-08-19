package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitWords(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"plain":              {"mkdir -p /opt/app", []string{"mkdir", "-p", "/opt/app"}},
		"extra whitespace":   {"  echo   hello\tworld ", []string{"echo", "hello", "world"}},
		"single quotes":      {`echo 'hello world'`, []string{"echo", "hello world"}},
		"double quotes":      {`echo "hello world"`, []string{"echo", "hello world"}},
		"quotes are literal": {`echo '$HOME "x"'`, []string{"echo", `$HOME "x"`}},
		"escaped quote":      {`echo "say \"hi\""`, []string{"echo", `say "hi"`}},
		"escaped dollar":     {`echo "\$HOME"`, []string{"echo", "$HOME"}},
		"bare backslash":     {`echo a\ b`, []string{"echo", "a b"}},
		"adjacent quoting":   {`echo a"b"c`, []string{"echo", "abc"}},
		"empty argument":     {`echo ""`, []string{"echo", ""}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := splitWords(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSplitWordsRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"unterminated double": `echo "unterminated`,
		"unterminated single": `echo 'unterminated`,
		"trailing backslash":  `echo hello \`,
		"empty":               "",
		"whitespace only":     "   ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := splitWords(in)
			require.ErrorIs(t, err, ErrBadHookSyntax, in)
		})
	}
}

// A hook is split here and executed without a shell, so shell metacharacters must survive as
// literal argument text rather than being interpreted.
func TestSplitWordsDoesNotInterpretMetacharacters(t *testing.T) {
	t.Parallel()
	got, err := splitWords(`echo a;rm -rf / && whoami`)
	require.NoError(t, err)
	require.Equal(t, []string{"echo", "a;rm", "-rf", "/", "&&", "whoami"}, got)
}
