package titlekey

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/unicode/norm"
)

func TestVersionFormat(t *testing.T) {
	v := Version()
	assert.Equal(t, "1/"+norm.Version, v)
	assert.Regexp(t, regexp.MustCompile(`^[1-9][0-9]*/[0-9]+\.[0-9]+\.[0-9]+$`), v)
}
