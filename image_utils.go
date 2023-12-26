package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/fogleman/gg"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/pemistahl/lingua-go"
)

const nSupportedScripts = 11

var (
	supportedScripts = [nSupportedScripts]language.Script{
		language.Unknown,
		language.Hiragana,
		language.Katakana,
		language.Hebrew,
		language.Thai,
		language.Arabic,
		language.Devanagari,
		language.Bengali,
		language.Javanese,
		language.Han,
		language.Hangul,
	}

	detector     lingua.LanguageDetector
	scriptRanges []ScriptRange
	fontMap      [nSupportedScripts]font.Face
	emojiFont    font.Face
)

type ScriptRange struct {
	Start  rune
	End    rune
	Pos    int
	Script language.Script
}

func initializeImageDrawingStuff() error {
	// language detector
	detector = lingua.NewLanguageDetectorBuilder().FromLanguages(
		lingua.Japanese,
		lingua.Persian,
		lingua.Chinese,
		lingua.Thai,
		lingua.Hebrew,
		lingua.Arabic,
		lingua.Bengali,
		lingua.Korean,
	).WithLowAccuracyMode().Build()

	// script detector material
	for ssi, script := range supportedScripts {
		for _, srange := range language.ScriptRanges {
			if srange.Script == script {
				scriptRanges = append(scriptRanges, ScriptRange{
					Start:  srange.Start,
					End:    srange.End,
					Script: srange.Script,
					Pos:    ssi,
				})
			}
		}
	}

	// fonts
	loadFont := func(filepath string) font.Face {
		fontData, err := fonts.ReadFile(filepath)
		face, err := font.ParseTTF(bytes.NewReader(fontData))
		if err != nil {
			log.Fatal().Err(err).Str("path", filepath).Msg("error loading font on startup")
			return nil
		}
		return face
	}
	fontMap[0] = loadFont("fonts/NotoSans.ttf")
	fontMap[1] = loadFont("fonts/NotoSansJP.ttf")
	fontMap[2] = fontMap[1]
	fontMap[3] = loadFont("fonts/NotoSansHebrew.ttf")
	fontMap[4] = loadFont("fonts/NotoSansThai.ttf")
	fontMap[5] = loadFont("fonts/NotoSansArabic.ttf")
	fontMap[6] = loadFont("fonts/NotoSansDevanagari.ttf")
	fontMap[7] = loadFont("fonts/NotoSansBengali.ttf")
	fontMap[8] = loadFont("fonts/NotoSansJavanese.ttf")
	fontMap[9] = loadFont("fonts/NotoSansSC.ttf")
	fontMap[10] = loadFont("fonts/NotoSansKR.ttf")
	emojiFont = loadFont("fonts/NotoEmoji.ttf")

	return nil
}

// quotesAsBlockPrefixedText replaces nostr:nevent1... and note with their text, as an extra line
// prefixed by BLOCK this returns a slice of lines
func quotesAsBlockPrefixedText(ctx context.Context, lines []string) []string {
	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()

	blocks := make([]string, 0, len(lines)+7)

	for _, line := range lines {
		matches := nostrNoteNeventMatcher.FindAllStringSubmatchIndex(line, -1)

		if len(matches) == 0 {
			// no matches, just return text as it is
			blocks = append(blocks, line)
			continue
		}

		// one or more matches, return multiple lines
		blocks = append(blocks, line[0:matches[0][0]])
		i := -1 // matches iteration counter
		b := 0  // current block index
		for _, match := range matches {
			i++

			matchText := line[match[0]:match[1]]
			submatch := nostrNoteNeventMatcher.FindStringSubmatch(matchText)
			nip19 := submatch[0][6:]

			event, _, err := getEvent(ctx, nip19, nil)
			if err != nil {
				// error case concat this to previous block
				blocks[b] += matchText
				continue
			}

			// add a new block with the quoted text
			blocks = append(blocks, BLOCK+" "+event.Content)

			// increase block count
			b++
		}
		// add remaining text after the last match
		remainingText := line[matches[i][1]:]
		if strings.TrimSpace(remainingText) != "" {
			blocks = append(blocks, remainingText)
		}
	}

	return blocks
}

func getLanguageAndScriptAndDirectionAndFont(paragraph []rune) (
	language.Language,
	language.Script,
	di.Direction,
	font.Face,
) {
	var ranking [nSupportedScripts]int
	nLetters := len(paragraph)
	threshold := nLetters / 2
	var script language.Script
	var face font.Face
	var idx int
	for l := 0; l < nLetters; l++ {
		idx := lookupScript(paragraph[l])
		ranking[idx]++
		if l > threshold && ranking[idx] > threshold {
			script = supportedScripts[idx]
			face = fontMap[idx]
			goto gotScript
		}
	}
	idx = maxIndex(ranking[:])
	script = supportedScripts[idx]
	face = fontMap[idx]

gotScript:
	direction := di.DirectionLTR
	if script == language.Arabic {
		direction = di.DirectionRTL
	}

	lng := language.Language("en-us")
	lang, ok := detector.DetectLanguageOf(string(paragraph))
	if ok {
		lng = language.Language(lang.IsoCode639_1().String())
	}

	return lng, script, direction, face
}

func fetchImageFromURL(url string) (image.Image, error) {
	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	img, _, err := image.Decode(response.Body)
	if err != nil {
		return nil, err
	}

	return img, nil
}

func roundImage(img image.Image) image.Image {
	bounds := img.Bounds()
	diameter := math.Min(float64(bounds.Dx()), float64(bounds.Dy()))
	radius := diameter / 2

	// Create a new context for the mask
	mask := gg.NewContext(bounds.Dx(), bounds.Dy())
	mask.SetColor(color.Black) // Set the mask color to fully opaque
	mask.DrawCircle(float64(bounds.Dx())/2, float64(bounds.Dy())/2, radius)
	mask.ClosePath()
	mask.Fill()

	// Apply the circular mask to the original image
	result := image.NewRGBA(bounds)
	maskImg := mask.Image()
	draw.DrawMask(result, bounds, img, image.Point{}, maskImg, image.Point{}, draw.Over)

	return result
}

func cropToSquare(img image.Image) image.Image {
	bounds := img.Bounds()
	size := int(math.Min(float64(bounds.Dx()), float64(bounds.Dy())))
	squareImg := image.NewRGBA(image.Rect(0, 0, size, size))
	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			squareImg.Set(x, y, img.At(x+(bounds.Dx()-size)/2, y+(bounds.Dy()-size)/2))
		}
	}
	return squareImg
}

func lookupScript(r rune) int {
	// binary search
	for i, j := 0, len(scriptRanges); i < j; {
		h := i + (j-i)/2
		entry := scriptRanges[h]
		if r < entry.Start {
			j = h
		} else if entry.End < r {
			i = h + 1
		} else {
			return h // position in supportedScripts
		}
	}
	return 0 // unknown
}
