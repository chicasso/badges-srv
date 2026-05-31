package mailer

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/ohhcgan/badges-srv/internal/stats"
	"github.com/ohhcgan/badges-srv/internal/user"
)

type Mailer struct {
	host     string
	port     int
	username string
	password string
	from     string
	fromAddr string /* bare address extracted from "Name <addr>" */
}

const (
	htmlTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
  body{margin:0;padding:0;background:#0D1117;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#E6EDF3}
  .wrap{max-width:680px;margin:0 auto;padding:32px 24px}
  .label{color:#3FB950;font-size:12px;font-weight:700;letter-spacing:2px;text-transform:uppercase;margin-bottom:6px}
  .title{font-size:28px;font-weight:700;color:#E6EDF3;margin:0 0 6px}
  .subtitle{font-size:14px;color:#7D8590;margin:0 0 28px}
  .poster{display:block;width:100%%;max-width:640px;border-radius:12px;margin:0 auto 28px}
  .stats{background:#161B22;border:1px solid #21262D;border-radius:10px;padding:20px 24px;margin-bottom:24px}
  .stats table{width:100%%;border-collapse:collapse}
  .stats td{padding:10px 0;font-size:15px;border-bottom:1px solid #21262D}
  .stats tr:last-child td{border-bottom:none}
  .stats .val{font-weight:700;text-align:right;color:#58A6FF}
  .pct{color:#8B949E;font-size:13px;margin:0 0 28px;padding:16px;background:#161B22;border-radius:8px;border-left:3px solid #3FB950}
  .footer{color:#7D8590;font-size:12px;border-top:1px solid #21262D;padding-top:20px;text-align:center}
</style>
</head>
<body>
<div class="wrap">
  <div class="label">GitHub Badges</div>
  <h1 class="title">Hi %s 👋</h1>
  <p class="subtitle">Here's your GitHub activity report for <strong>%s</strong>.</p>
  <img class="poster" src="cid:poster@github-badges" alt="GitHub Activity Report for %s">
  <div class="stats">
    <table>
      <tr><td>Total Commits</td><td class="val">%s</td></tr>
      <tr><td>New Repositories Created</td><td class="val">%d</td></tr>
      <tr><td>Open-Source Contributions (PRs)</td><td class="val">%d</td></tr>
    </table>
  </div>
  <p class="pct">%s</p>
  <div class="footer">
    You're receiving this because you signed up for GitHub Badges monthly reports.<br>
    &copy; %d GitHub Badges
  </div>
</div>
</body>
</html>`
)

func New(
	emailHost, username, password, emailFrom string,
	smtpPort int,
) *Mailer {
	return &Mailer{
		host:     emailHost,
		port:     smtpPort,
		username: username,
		password: password,
		from:     emailFrom,
		fromAddr: extractAddr(emailFrom),
	}
}

func (m *Mailer) SendPoster(userInfo *user.User, statsInfo *stats.MonthlyStats, posterPNG []byte) error {
	if userInfo.Email == "" {
		return errors.New("email must be provided")
	}

	subject := fmt.Sprintf("Your GitHub Activity Report - %s", statsInfo.StatMonth.Format("January 2006"))
	htmlBody := buildHTML(userInfo, statsInfo)
	msg, err := buildMIME(m.from, userInfo.Email, subject, htmlBody, posterPNG)
	if err != nil {
		return fmt.Errorf("could not build mime message: %w", err)
	}

	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	return smtp.SendMail(addr, auth, m.fromAddr, []string{userInfo.Email}, msg)
}

/**
 * buildMIME constructs a multipart/related MIME message with an HTML body
 * referencing the poster image via CID, plus the image as a base64 inline part.
 */
func buildMIME(from, to, subject, htmlBody string, posterPNG []byte) ([]byte, error) {
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)

	/* HTML part */
	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, _ := mw.CreatePart(htmlHeader)
	_, err := htmlPart.Write([]byte(htmlBody))
	if err != nil {
		return nil, fmt.Errorf("error creating multipart body: %w", err)
	}

	/* Inline PNG part referenced by CID */
	imgHeader := textproto.MIMEHeader{}
	imgHeader.Set("Content-Type", `image/png; name="github-activity.png"`)
	imgHeader.Set("Content-Transfer-Encoding", "base64")
	imgHeader.Set("Content-ID", "<poster@github-badges>")
	imgHeader.Set("Content-Disposition", `inline; filename="github-activity.png"`)
	imgPart, _ := mw.CreatePart(imgHeader)

	enc := base64.NewEncoder(base64.StdEncoding, imgPart)
	_, err = enc.Write(posterPNG)
	if err != nil {
		return nil, fmt.Errorf("error creating base64 encoder: %w", err)
	}

	err = enc.Close()
	if err != nil {
		_ = mw.Close()
		return nil, fmt.Errorf("error creating base64 encoder: %w", err)
	}

	err = mw.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing multipart writer: %w", err)
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/related; boundary=%q\r\n\r\n", mw.Boundary())
	msg.Write(bodyBuf.Bytes())

	return msg.Bytes(), nil
}

/**
 * TODO:
 * Use aws s3 sdk to upload png so that user can download anytime.
 *
 * buildHTML returns the HTML email body. The poster image is displayed inline
 * via CID so it renders directly inside the email.
 */
func buildHTML(u *user.User, st *stats.MonthlyStats) string {
	displayName := u.Name
	if displayName == "" {
		displayName = u.GithubLogin
	}

	pctLine := "First tracked month — no comparison available."
	if st.CommitPctChange.Valid {
		v := st.CommitPctChange.Float64
		sign := "+"
		if v < 0 {
			sign = "-"
		} else if v == 0 {
			sign = ""
		}

		pctLine = fmt.Sprintf("Your commits %s%.1f%% vs last month.", sign, v)
	}

	return fmt.Sprintf(
		htmlTemplate,
		displayName,
		st.StatMonth.Format("January 2006"),
		st.StatMonth.Format("January 2006"),
		formatNumber(st.TotalCommits),
		st.ReposCreated,
		st.OpenSourceContributions,
		pctLine,
		time.Now().Year(),
	)
}

func formatNumber(num int) string {
	numFmt := fmt.Sprintf("%d", num)
	if len(numFmt) <= 3 {
		return numFmt
	}

	var buff strings.Builder
	for i, c := range []byte(numFmt) {
		if i > 0 && (len(numFmt)-i)%3 == 0 {
			buff.WriteByte(',')
		}
		buff.WriteByte(c)
	}
	return buff.String()
}

/**
 * extractAddr return only the email address from full address "Name <addr@host>".
 */
func extractAddr(fullAddr string) string {
	start := strings.Index(fullAddr, "<")
	end := strings.Index(fullAddr, ">")
	if start != -1 && end != -1 && end > start {
		return strings.TrimSpace(fullAddr[start+1 : end])
	}
	return strings.TrimSpace(fullAddr)
}
