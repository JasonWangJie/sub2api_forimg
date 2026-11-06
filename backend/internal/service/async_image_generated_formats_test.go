package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func generatedAnimatedGIFFixture(t *testing.T, frames int) []byte {
	t.Helper()
	animation := &gif.GIF{LoopCount: 0}
	for i := 0; i < frames; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 2, 3), color.Palette{color.Black, color.White})
		frame.SetColorIndex(i%2, i%3, 1)
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 20+i)
		animation.Disposal = append(animation.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buf, animation))
	return buf.Bytes()
}

func TestGeneratedGIFPreservesAnimationAndBoundsAllFrames(t *testing.T) {
	data := generatedAnimatedGIFFixture(t, 3)
	validated, err := ValidateGeneratedImageBytes(data, int64(len(data)), 18)
	require.NoError(t, err)
	require.Equal(t, "image/gif", validated.MIMEType)
	require.Equal(t, data, validated.Data)
	require.Equal(t, 2, validated.Width)
	require.Equal(t, 3, validated.Height)
	decoded, err := gif.DecodeAll(bytes.NewReader(validated.Data))
	require.NoError(t, err)
	require.Len(t, decoded.Image, 3)
	require.Equal(t, []int{20, 21, 22}, decoded.Delay)
	require.Equal(t, 0, decoded.LoopCount)
	_, err = ValidateImageBytes(data, "image/gif", 1<<20, 100)
	require.Error(t, err, "input policy must still reject GIF")
	for name, invalid := range map[string][]byte{
		"trailing":        append(append([]byte(nil), data...), []byte("<script/>")...),
		"truncated":       data[:len(data)-1],
		"17 frames":       generatedAnimatedGIFFixture(t, 17),
		"bad later frame": append([]byte(nil), data...),
	} {
		t.Run(name, func(t *testing.T) {
			if name == "bad later frame" {
				// Change the last frame's width while keeping the canvas valid.
				for pos := len(invalid) - 10; pos >= 13; pos-- {
					if invalid[pos] == 0x2c {
						binary.LittleEndian.PutUint16(invalid[pos+5:], 0xffff)
						break
					}
				}
			}
			_, err := ValidateGeneratedImageBytes(invalid, 1<<20, 1000)
			require.Error(t, err)
		})
	}
	_, err = ValidateGeneratedImageBytes(data, int64(len(data)-1), 100)
	require.Error(t, err)
	_, err = ValidateGeneratedImageBytes(data, 1<<20, 17)
	require.Error(t, err, "all frames count toward the pixel limit")
	data16 := generatedAnimatedGIFFixture(t, 16)
	_, err = ValidateGeneratedImageBytes(data16, 1<<20, 96)
	require.NoError(t, err)
}

func TestGeneratedSVGPassiveContentAndSafety(t *testing.T) {
	safe := "<svg xmlns=\"http://www.w3.org/2000/svg\" xmlns:xlink=\"http://www.w3.org/1999/xlink\" viewBox=\"0 0 10 20\"><defs><linearGradient id=\"paint\"><stop offset=\"0\" stop-color=\"red\"/></linearGradient><path id=\"shape\" d=\"M0 0 L5 5\"/></defs><style>.shape { fill: url(#paint); stroke: black; }</style><g class=\"shape\" style=\"opacity: 0.5\"><use xlink:href=\"#shape\"/><text x=\"1\" y=\"2\">猫 &amp; cat</text></g></svg>"
	for _, data := range [][]byte{[]byte(safe), append([]byte{0xef, 0xbb, 0xbf}, []byte("<?xml version=\"1.0\"?>"+safe)...), []byte("<svg width=\"100%\" height=\"100%\" viewBox=\"0 0 10 20\"><rect fill=\"burlywood\"/></svg>")} {
		got, err := ValidateGeneratedImageBytes(data, int64(len(data)), 200)
		require.NoError(t, err)
		require.Equal(t, data, got.Data)
		require.Equal(t, "image/svg+xml", got.MIMEType)
		require.Equal(t, "svg", got.Format)
		require.Equal(t, 10, got.Width)
		require.Equal(t, 20, got.Height)
		_, err = ValidateImageBytes(data, "image/svg+xml", 1<<20, 1000)
		require.Error(t, err, "input policy must still reject SVG")
	}
	wrap := func(content string) string {
		return "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"10\" height=\"20\">" + content + "</svg>"
	}
	cases := map[string]string{
		"script":                    wrap("<script>alert(1)</script>"),
		"event":                     wrap("<path onload=\"alert(1)\"/>"),
		"foreignObject":             wrap("<foreignObject><div/></foreignObject>"),
		"animation":                 wrap("<animate attributeName=\"href\" values=\"javascript:alert(1)\"/>"),
		"external use":              wrap("<use href=\"https://evil.test/x.svg#x\"/>"),
		"entity encoded javascript": wrap("<use href=\"java&#115;cript:alert(1)\"/>"),
		"external paint":            wrap("<rect fill=\"url(https://evil.test/x)\"/>"),
		"external style":            wrap("<style>.x {fill:url(https://evil.test/x)}</style>"),
		"CSS import":                wrap("<style>@import 'https://evil.test/x';</style>"),
		"CSS escape":                wrap("<rect style=\"fill:u\\72l(https://evil.test/x)\"/>"),
		"CSS comments":              wrap("<rect style=\"fill:u/**/rl(https://evil.test/x)\"/>"),
		"CSS image":                 wrap("<rect style=\"background-image: image-set('https://evil.test/x')\"/>"),
		"XML base":                  "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1\" height=\"1\" xml:base=\"https://evil.test/\"><use href=\"#x\"/></svg>",
		"doctype":                   "<!DOCTYPE svg [<!ENTITY x SYSTEM \"file:///secret\">]>" + wrap("&x;"),
		"processing instruction":    "<?xml-stylesheet href=\"https://evil.test/x\"?>" + wrap(""),
		"wrong namespace":           "<svg xmlns=\"http://www.w3.org/1999/xhtml\" width=\"1\" height=\"1\"/>",
		"duplicate attr":            "<svg width=\"1\" width=\"2\" height=\"1\"/>",
		"trailing markup":           safe + "<script/>",
		"trailing text":             safe + "text",
		"truncated":                 safe[:len(safe)-1],
		"missing dimensions":        "<svg/>",
		"invalid units":             "<svg width=\"2cm\" height=\"3\" viewBox=\"0 0 2 3\"/>",
		"invalid viewBox":           "<svg width=\"2\" height=\"3\" viewBox=\"NaN 0 2 3\"/>",
		"nan":                       "<svg width=\"NaN\" height=\"2\"/>",
		"infinite":                  "<svg width=\"Inf\" height=\"2\"/>",
		"oversized":                 "<svg width=\"10000000\" height=\"10000000\"/>",
		"deep nesting":              wrap(strings.Repeat("<g>", 130) + strings.Repeat("</g>", 130)),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateGeneratedImageBytes([]byte(input), 1<<20, 1000)
			require.Error(t, err)
		})
	}
	_, err := ValidateGeneratedImageBytes([]byte(safe), 1<<20, 199)
	require.Error(t, err)
}

func TestGeneratedExtendedFormatDownloadAndDataURI(t *testing.T) {
	for _, fixture := range []struct {
		data []byte
		mime string
	}{
		{generatedAnimatedGIFFixture(t, 2), "image/gif"},
		{[]byte("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"2px\" height=\"3\"><rect width=\"2\" height=\"3\" fill=\"red\"/></svg>"), "image/svg+xml"},
	} {
		t.Run(fixture.mime, func(t *testing.T) {
			d := AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 100}
			ref, err := d.DownloadGenerated(context.Background(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(fixture.data))
			require.NoError(t, err)
			require.Equal(t, fixture.data, ref.Data)
			require.Equal(t, fixture.mime, ref.MIMEType)
			client := &http.Client{Transport: generatedImageRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				require.Empty(t, req.Header.Get("Authorization"))
				return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(bytes.NewReader(fixture.data))}, nil
			})}
			parsed, err := url.Parse("https://cdn.example/result?sig=secret")
			require.NoError(t, err)
			ref, err = d.fetchRemoteImage(context.Background(), parsed, client, true)
			require.NoError(t, err)
			require.Equal(t, fixture.data, ref.Data)
			require.Equal(t, fixture.mime, ref.MIMEType)
		})
	}
}
