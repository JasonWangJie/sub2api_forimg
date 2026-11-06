package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image/gif"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ValidateGeneratedImageBytes accepts GIF and passive SVG only for generated
// results. Input images keep the existing ValidateImageBytes format policy.
// Original bytes, including all animation frames and timing, are preserved.
func ValidateGeneratedImageBytes(data []byte, maxBytes, maxPixels int64) (*ValidatedImage, error) {
	if maxBytes <= 0 {
		maxBytes = defaultAsyncImageReferenceMaxBytes
	}
	if maxPixels <= 0 {
		maxPixels = defaultAsyncImageReferenceMaxPixels
	}
	if len(data) == 0 || int64(len(data)) > maxBytes {
		return nil, errors.New("generated image is empty or exceeds the configured byte limit")
	}
	var width, height int
	var format, mimeType string
	var err error
	switch {
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		width, height, err = validateGeneratedGIF(data, maxPixels)
		format, mimeType = "gif", "image/gif"
	case bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})), []byte("<")):
		width, height, err = validateGeneratedSVG(data, maxPixels)
		format, mimeType = "svg", "image/svg+xml"
	default:
		return ValidateImageBytes(data, "", maxBytes, maxPixels)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid generated %s image: %w", format, err)
	}
	sum := sha256.Sum256(data)
	return &ValidatedImage{Data: data, MIMEType: mimeType, Format: format, Width: width, Height: height,
		SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))}, nil
}

func (d AsyncImageReferenceDownloader) ValidateGeneratedBytes(data []byte) (*AsyncImageReference, error) {
	v, err := ValidateGeneratedImageBytes(data, d.maxBytes(), d.maxPixels())
	if err != nil {
		return nil, err
	}
	return &AsyncImageReference{Data: v.Data, MIMEType: v.MIMEType, Width: v.Width, Height: v.Height, SHA256: v.SHA256}, nil
}

// Scan before DecodeAll allocates frame buffers. DecodeConfig alone cannot
// bound frame count or the aggregate decoded pixel allocation.
func validateGeneratedGIF(data []byte, maxPixels int64) (int, int, error) {
	bad := errors.New("invalid GIF container, frame limit, pixel limit or trailing data")
	if len(data) < 13 {
		return 0, 0, bad
	}
	w, h := int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
	if w <= 0 || h <= 0 || int64(w)*int64(h) > maxPixels {
		return 0, 0, bad
	}
	pos, frames := 13, 0
	var pixels int64
	if data[10]&0x80 != 0 {
		pos += 3 * (1 << ((data[10] & 7) + 1))
	}
	skipBlocks := func() bool {
		for pos < len(data) {
			n := int(data[pos])
			pos++
			if n == 0 {
				return true
			}
			pos += n
		}
		return false
	}
	for pos < len(data) {
		kind := data[pos]
		pos++
		switch kind {
		case 0x21:
			if pos >= len(data) {
				return 0, 0, bad
			}
			pos++ // extension label
			if !skipBlocks() {
				return 0, 0, bad
			}
		case 0x2c:
			if pos+9 > len(data) {
				return 0, 0, bad
			}
			x, y := int(binary.LittleEndian.Uint16(data[pos:])), int(binary.LittleEndian.Uint16(data[pos+2:]))
			fw, fh := int(binary.LittleEndian.Uint16(data[pos+4:])), int(binary.LittleEndian.Uint16(data[pos+6:]))
			frames++
			pixels += int64(fw) * int64(fh)
			if fw <= 0 || fh <= 0 || x+fw > w || y+fh > h || frames > 16 || pixels > maxPixels {
				return 0, 0, bad
			}
			packed := data[pos+8]
			pos += 9
			if packed&0x80 != 0 {
				pos += 3 * (1 << ((packed & 7) + 1))
			}
			pos++ // LZW minimum code size checked by DecodeAll
			if !skipBlocks() {
				return 0, 0, bad
			}
		case 0x3b:
			if pos != len(data) || frames == 0 {
				return 0, 0, bad
			}
			decoded, err := gif.DecodeAll(bytes.NewReader(data))
			if err != nil || len(decoded.Image) != frames {
				return 0, 0, errors.New("GIF frames could not be fully decoded")
			}
			return w, h, nil
		default:
			return 0, 0, bad
		}
	}
	return 0, 0, bad
}

const svgNamespace = "http://www.w3.org/2000/svg"

var passiveSVGElements = generatedSVGSet("svg g defs title desc path rect circle ellipse line polyline polygon text tspan textPath use symbol linearGradient radialGradient stop clipPath mask pattern marker filter feBlend feColorMatrix feComponentTransfer feComposite feConvolveMatrix feDiffuseLighting feDisplacementMap feDistantLight feDropShadow feFlood feFuncA feFuncB feFuncG feFuncR feGaussianBlur feMerge feMergeNode feMorphology feOffset fePointLight feSpecularLighting feSpotLight feTile feTurbulence style")
var passiveSVGAttributes = generatedSVGSet("id class style version x y x1 x2 y1 y2 dx dy width height viewBox preserveAspectRatio d points cx cy r rx ry transform opacity fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset clip-rule clip-path mask filter color display visibility font-family font-size font-style font-weight font-variant text-anchor dominant-baseline alignment-baseline letter-spacing word-spacing text-decoration textLength lengthAdjust rotate startOffset method spacing href gradientUnits gradientTransform spreadMethod offset stop-color stop-opacity fx fy fr clipPathUnits maskUnits maskContentUnits patternUnits patternContentUnits patternTransform markerWidth markerHeight markerUnits refX refY orient marker-start marker-mid marker-end filterUnits primitiveUnits in in2 result type values stdDeviation mode operator k1 k2 k3 k4 order kernelMatrix divisor bias targetX targetY edgeMode kernelUnitLength preserveAlpha surfaceScale diffuseConstant specularConstant specularExponent limitingConeAngle scale xChannelSelector yChannelSelector azimuth elevation z pointsAtX pointsAtY pointsAtZ baseFrequency numOctaves seed stitchTiles amplitude exponent intercept slope tableValues flood-color flood-opacity lighting-color color-interpolation color-interpolation-filters vector-effect paint-order shape-rendering text-rendering overflow enable-background")
var passiveSVGStyleProperties = generatedSVGSet("fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset clip-rule clip-path mask filter color opacity display visibility font-family font-size font-style font-weight font-variant text-anchor dominant-baseline alignment-baseline letter-spacing word-spacing text-decoration stop-color stop-opacity flood-color flood-opacity lighting-color color-interpolation color-interpolation-filters vector-effect paint-order shape-rendering text-rendering overflow")
var svgFragment = regexp.MustCompile("^#[A-Za-z_][A-Za-z0-9_.:-]*$")
var svgCSSSelector = regexp.MustCompile("^[A-Za-z0-9_ .,#:>+*\\-\\s]+$")
var svgURLFunction = regexp.MustCompile("(?i)url\\s*\\(")

func generatedSVGSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}

func validateGeneratedSVG(data []byte, maxPixels int64) (int, int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	depth, nodes, width, height := 0, 0, 0, 0
	seenRoot, ended := false, false
	styleDepth := 0
	var styleText strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, errors.New("malformed SVG XML")
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			nodes++
			if ended || depth > 128 || nodes > 100000 || styleDepth != 0 || !passiveSVGElements[value.Name.Local] || (value.Name.Space != "" && value.Name.Space != svgNamespace) {
				return 0, 0, errors.New("SVG contains unsupported elements or exceeds complexity limits")
			}
			if !seenRoot {
				if value.Name.Local != "svg" {
					return 0, 0, errors.New("SVG root is required")
				}
				width, height, err = generatedSVGDimensions(value.Attr, maxPixels)
				if err != nil {
					return 0, 0, err
				}
				seenRoot = true
			}
			seenAttrs := make(map[xml.Name]bool)
			for _, attr := range value.Attr {
				if seenAttrs[attr.Name] || !passiveSVGAttribute(attr) {
					return 0, 0, errors.New("SVG contains unsupported or unsafe attributes")
				}
				seenAttrs[attr.Name] = true
			}
			if value.Name.Local == "style" {
				styleDepth = depth
				styleText.Reset()
			}
		case xml.EndElement:
			if styleDepth == depth {
				if !passiveSVGStylesheet(styleText.String()) {
					return 0, 0, errors.New("SVG contains unsafe styles")
				}
				styleDepth = 0
			}
			depth--
			if depth == 0 {
				ended = true
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return 0, 0, errors.New("SVG contains trailing content")
			}
			if styleDepth != 0 {
				styleText.Write(value)
			}
		case xml.Directive:
			return 0, 0, errors.New("SVG directives and entities are not supported")
		case xml.ProcInst:
			if value.Target != "xml" || seenRoot {
				return 0, 0, errors.New("SVG processing instructions are not supported")
			}
		}
	}
	if !seenRoot || !ended || depth != 0 {
		return 0, 0, errors.New("incomplete SVG document")
	}
	return width, height, nil
}

func passiveSVGAttribute(attr xml.Attr) bool {
	if attr.Name.Space == "xmlns" || (attr.Name.Space == "" && attr.Name.Local == "xmlns") {
		return attr.Value == svgNamespace || attr.Value == "http://www.w3.org/1999/xlink"
	}
	if attr.Name.Space == "http://www.w3.org/XML/1998/namespace" {
		return attr.Name.Local == "space" && (attr.Value == "preserve" || attr.Value == "default")
	}
	if attr.Name.Space != "" && !(attr.Name.Space == "http://www.w3.org/1999/xlink" && attr.Name.Local == "href") {
		return false
	}
	if !passiveSVGAttributes[attr.Name.Local] {
		return false
	}
	if attr.Name.Local == "href" {
		return svgFragment.MatchString(strings.TrimSpace(attr.Value))
	}
	if attr.Name.Local == "style" {
		return passiveSVGDeclarations(attr.Value)
	}
	return passiveSVGValue(attr.Value)
}

func passiveSVGValue(value string) bool {
	lower := strings.ToLower(value)
	// CSS escapes, comments, at-rules and markup cannot hide external URLs.
	if strings.ContainsAny(value, "\\@<>{}\x00") || strings.Contains(lower, "/*") || strings.Contains(lower, "//") || strings.Contains(lower, "expression") || strings.Contains(lower, "javascript") || strings.Contains(lower, "data:") {
		return false
	}
	for _, r := range value {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	for {
		location := svgURLFunction.FindStringIndex(value)
		if location == nil {
			return true
		}
		rest := value[location[1]-1:]
		end := strings.IndexByte(rest, ')')
		if end < 0 || !svgFragment.MatchString(strings.Trim(strings.TrimSpace(rest[1:end]), "\"'")) {
			return false
		}
		value = rest[end+1:]
	}
}

func passiveSVGDeclarations(value string) bool {
	for _, declaration := range strings.Split(value, ";") {
		if strings.TrimSpace(declaration) == "" {
			continue
		}
		key, val, ok := strings.Cut(declaration, ":")
		if !ok || !passiveSVGStyleProperties[strings.TrimSpace(strings.ToLower(key))] || !passiveSVGValue(val) {
			return false
		}
	}
	return true
}

func passiveSVGStylesheet(value string) bool {
	for strings.TrimSpace(value) != "" {
		selector, rest, ok := strings.Cut(value, "{")
		if !ok || !svgCSSSelector.MatchString(strings.TrimSpace(selector)) {
			return false
		}
		declarations, remaining, ok := strings.Cut(rest, "}")
		if !ok || !passiveSVGDeclarations(declarations) {
			return false
		}
		value = remaining
	}
	return true
}

func generatedSVGDimensions(attrs []xml.Attr, maxPixels int64) (int, int, error) {
	var width, height, viewWidth, viewHeight float64
	bad := errors.New("SVG needs valid width/height or viewBox within the configured pixel limit")
	parseLength := func(value string) (float64, error) {
		value = strings.TrimSpace(value)
		percentage := strings.HasSuffix(value, "%")
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(value, "px"), "%")), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed <= 0 {
			return 0, bad
		}
		if percentage {
			if parsed > 100 {
				return 0, bad
			}
			return 0, nil // responsive dimensions require the intrinsic viewBox
		}
		return parsed, nil
	}
	for _, attr := range attrs {
		if attr.Name.Space != "" {
			continue
		}
		switch attr.Name.Local {
		case "width":
			var err error
			width, err = parseLength(attr.Value)
			if err != nil {
				return 0, 0, err
			}
		case "height":
			var err error
			height, err = parseLength(attr.Value)
			if err != nil {
				return 0, 0, err
			}
		case "viewBox":
			parts := strings.Fields(strings.ReplaceAll(attr.Value, ",", " "))
			if len(parts) != 4 {
				return 0, 0, bad
			}
			values := make([]float64, 4)
			for i, part := range parts {
				v, err := strconv.ParseFloat(part, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return 0, 0, bad
				}
				values[i] = v
			}
			viewWidth, viewHeight = values[2], values[3]
			if viewWidth <= 0 || viewHeight <= 0 {
				return 0, 0, bad
			}
		}
	}
	if width == 0 {
		width = viewWidth
	}
	if height == 0 {
		height = viewHeight
	}
	width, height = math.Ceil(width), math.Ceil(height)
	if math.IsNaN(width) || math.IsNaN(height) || width <= 0 || height <= 0 || width > float64(maxPixels) || height > float64(maxPixels) || width*height > float64(maxPixels) {
		return 0, 0, bad
	}
	return int(width), int(height), nil
}
