package poster

import (
	"bytes"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/ohhcgan/badges-srv/internal/stats"
	"github.com/ohhcgan/badges-srv/internal/user"
)

/* canvasW and canvasH matches the standard Open Graph image ratio. */
const (
	canvasW = 1200
	canvasH = 630
)

/**
 * TODO: add light as well
 */
/* GitHub dark mode colour palette. */
var (
	colBG      = hexRGB("#0D1117")
	colSurface = hexRGB("#161B22")
	colBorder  = hexRGB("#30363D")
	colText    = hexRGB("#E6EDF3")
	colMuted   = hexRGB("#7D8590")
	colGreen   = hexRGB("#3FB950")
	colBlue    = hexRGB("#58A6FF")
	colAmber   = hexRGB("#D29922")
	colRed     = hexRGB("#F85149")
)

var (
	fontSmall        float64 = 7
	fontRegularSmall float64 = 9
	fondMedium       float64 = 11
	fontLarge        float64 = 22
	fontExtraLarge   float64 = 28
)

type Generator struct {
	boldSM     font.Face /* ~20px — card labels, footer */
	boldMD     font.Face /* ~32px — profile name, header label */
	boldLG     font.Face /* ~52px — stat numbers */
	boldXL     font.Face /* ~64px — month header */
	regSM      font.Face /* ~20px — profile username */
	httpClient *http.Client
}

type CardData struct {
	label    string
	value    string
	subtitle string
	accent   color.Color
}

func NewGenerator() (*Generator, error) {
	boldFont, err := truetype.Parse(gobold.TTF)
	if err != nil {
		return nil, fmt.Errorf("parsing bold font: %w", err)
	}
	regularFont, err := truetype.Parse(goregular.TTF)
	if err != nil {
		return nil, fmt.Errorf("parsing regular font: %w", err)
	}

	face := func(f *truetype.Font, size float64) font.Face {
		return truetype.NewFace(f, &truetype.Options{
			Size:    size,
			DPI:     144,
			Hinting: font.HintingFull,
		})
	}

	return &Generator{
		boldSM:     face(boldFont, fontSmall),
		boldMD:     face(boldFont, fondMedium),
		boldLG:     face(boldFont, fontLarge),
		boldXL:     face(boldFont, fontExtraLarge),
		regSM:      face(regularFont, fontRegularSmall),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (g *Generator) Generate(u *user.User, st *stats.MonthlyStats) ([]byte, error) {
	dc := gg.NewContext(canvasW, canvasH)

	/* Background */
	dc.SetColor(colBG)
	dc.Clear()

	/* Top accent bar */
	dc.SetColor(colGreen)
	dc.DrawRectangle(0, 0, canvasW, 8)
	dc.Fill()

	/* Header - title section */
	dc.SetFontFace(g.boldSM)
	setColor(dc, colGreen)
	dc.DrawString("GITHUB ACTIVITY REPORT", 60, 50)

	/* Header - month section */
	dc.SetFontFace(g.boldXL)
	setColor(dc, colText)
	dc.DrawString(st.StatMonth.Format("January 2006"), 60, 110)

	/* Header - right side username */
	dc.SetFontFace(g.boldMD)
	setColor(dc, colMuted)
	dc.DrawStringAnchored("@"+u.GithubLogin, canvasW-60, 50, 1, 0.5)

	/* Header - right side poweredby */
	dc.SetFontFace(g.regSM)
	setColor(dc, colMuted)
	dc.DrawStringAnchored("Powered by GitHub Badges", canvasW-60, 75, 1, 0.5)

	/* Divider */
	dc.SetColor(colBorder)
	dc.SetLineWidth(1)
	dc.DrawLine(60, 140, canvasW-60, 140)
	dc.Stroke()

	/* Profile */
	const (
		avatarRadius  = 70
		avatarCenterX = 60 + (avatarRadius)
		avatarCenterY = 240
	)

	avatar, err := g.downloadAvatar(u.AvatarURL, avatarRadius*2)
	if err == nil {
		/* render profile pic */
		dc.DrawCircle(avatarCenterX, avatarCenterY, avatarRadius)
		dc.Clip()
		dc.DrawImageAnchored(avatar, avatarCenterX, avatarCenterY, 0.5, 0.5)
		dc.ResetClip()
	} else {
		/* fallback using username initials */
		dc.SetColor(colSurface)
		dc.DrawCircle(avatarCenterX, avatarCenterY, avatarRadius)
		dc.Fill()
		dc.SetFontFace(g.boldLG)
		setColor(dc, colText)
		dc.DrawStringAnchored(initials(u.Name, u.GithubLogin), avatarCenterX, avatarCenterY, 0.5, 0.5)
	}

	/* Name & login */
	const (
		nameX = 60 + avatarRadius*2 + 60
		nameY = 240 - avatarRadius/2

		loginX = 60 + avatarRadius*2 + 60
		loginY = 240 - avatarRadius/2 + 30
	)
	dc.SetFontFace(g.boldMD)
	dc.SetColor(colText)
	displayName := u.Name
	if displayName == "" {
		displayName = u.GithubLogin
	}
	dc.DrawString(displayName, nameX, nameY)

	dc.SetFontFace(g.regSM)
	setColor(dc, colMuted)
	dc.DrawString("@"+u.GithubLogin, loginX, loginY)

	/* Stat Cards */
	const (
		cardY = 345
		cardH = 200
		cardW = 200
		gap   = 20
	)

	pctStr, pctPositive := formatPercentageCommit(st.CommitPctChange)
	pctAccent := colGreen
	if !pctPositive {
		pctAccent = colRed
	}

	cards := []CardData{
		{
			label:    "COMMITS",
			value:    formatComma(st.TotalCommits),
			subtitle: st.StatMonth.Format("January 2006"),
			accent:   colBlue,
		},
		{
			label:    "NEW REPOS",
			value:    formatComma(st.ReposCreated),
			subtitle: fmt.Sprintf("Created: %s", st.StatMonth.Format("01 2006")),
			accent:   colGreen,
		},
		{
			label:    "OSS CONTRIBUTIONS",
			value:    formatComma(st.OpenSourceContributions),
			subtitle: fmt.Sprintf("OSS: %s", st.StatMonth.Format("01 2006")),
			accent:   colAmber,
		},
		{
			label:    "COMMIT GROWTH",
			value:    pctStr,
			subtitle: "Vs. previous month",
			accent:   pctAccent,
		},
	}

	startX := 60.0
	for i, card := range cards {
		cx := startX + float64(i)*(cardW+gap)
		g.drawCard(dc, cx, cardY, cardW, cardH, card.label, card.value, card.subtitle, card.accent)
	}

	/* Footer */
	setColor(dc, colBorder)
	dc.SetLineWidth(1)
	dc.DrawLine(60, 570, canvasW-60, 570)
	dc.Stroke()

	dc.SetFontFace(g.boldSM)
	setColor(dc, colMuted)
	dc.DrawString("github-badges · Monthly Activity Report", 60, 600)
	dc.DrawStringAnchored(
		fmt.Sprintf("Generated %s", time.Now().UTC().Format("2 Jan 2006")),
		canvasW-60, 600, 1, 0.5,
	)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("encoding poster PNG: %w", err)
	}
	return buf.Bytes(), nil
}

/**
 * drawCard renders a single stat card onto the drawing context.
 */
func (g *Generator) drawCard(dc *gg.Context, x, y, w, h float64, label, value, subtitle string, accent color.Color) {
	/* Border */
	dc.SetColor(colBorder)
	dc.DrawRoundedRectangle(x-1, y-1, w+2, h+2, 13)
	dc.Fill()

	/* Background */
	dc.SetColor(colSurface)
	dc.DrawRoundedRectangle(x, y, w, h, 12)
	dc.Fill()

	/* Top accent stripe (clip to card shape first) */
	dc.DrawRoundedRectangle(x, y, w, h, 12)
	dc.Clip()
	dc.SetColor(accent)
	dc.DrawRectangle(x, y, w, 4)
	dc.Fill()
	dc.ResetClip()

	/* Label */
	dc.SetFontFace(g.boldSM)
	setColor(dc, colMuted)
	dc.DrawString(label, x+16, y+32)

	/* Value */
	dc.SetFontFace(g.boldLG)
	dc.SetColor(accent)
	dc.DrawStringAnchored(value, x+w/2, y+105, 0.5, 0.5)

	/* Subtitle */
	dc.SetFontFace(g.regSM)
	setColor(dc, colMuted)
	dc.DrawStringAnchored(subtitle, x+w/2, y+152, 0.5, 0.5)
}

func (g *Generator) downloadAvatar(avatarURL string, size int) (image.Image, error) {
	resp, err := g.httpClient.Get(avatarURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	src, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, err
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst, nil
}

/**
 * Helpers
 */
func setColor(dc *gg.Context, c color.Color) {
	dc.SetColor(c)
}

func hexRGB(hex string) color.RGBA {
	hex = hex[1:] /* strip '#' */

	red, _ := strconv.ParseUint(hex[0:2], 16, 8)
	green, _ := strconv.ParseUint(hex[2:4], 16, 8)
	blue, _ := strconv.ParseUint(hex[4:6], 16, 8)

	return color.RGBA{R: uint8(red), G: uint8(green), B: uint8(blue), A: 255}
}

/**
 * TODO: can use Indian comma system here
 */
func formatComma(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var out []byte
	for i, ch := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, ch)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func formatPercentageCommit(v sql.NullFloat64) (string, bool) {
	if !v.Valid {
		return "--", true
	}
	val := v.Float64
	if val >= 0 {
		return fmt.Sprintf("+%.1f%%", val), true
	}
	return fmt.Sprintf("%.1f%%", val), false
}

func initials(name, login string) string {
	src := name
	if src == "" {
		src = login
	}
	parts := []rune(src)
	if len(parts) == 0 {
		return "?"
	}
	if len(parts) == 1 {
		return string(parts[0])
	}

	chars := []rune{}
	inWord := false
	for _, r := range parts {
		if r == ' ' {
			inWord = false
			continue
		}
		if !inWord {
			chars = append(chars, r)
			inWord = true
		}
		if len(chars) == 2 {
			break
		}
	}
	return string(chars)
}
