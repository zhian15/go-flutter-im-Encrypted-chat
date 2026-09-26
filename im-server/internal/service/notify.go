package service

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ============ 阿里云短信（国内 + 国际） ============

func sendSMSCode(accessKey, secretKey, signName, templateCode, countryCode, phone, code string) error {
	if accessKey == "" || templateCode == "" {
		return fmt.Errorf("aliyun sms not configured")
	}
	cc := countryCode
	if cc == "" {
		cc = "+86"
	}
	params := map[string]string{
		"Action":           "SendSms",
		"Version":          "2017-05-25",
		"RegionId":         "cn-hangzhou",
		"AccessKeyId":      accessKey,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   fmt.Sprintf("%d", nonce()),
		"Timestamp":        timeNowISO(),
		"Format":           "JSON",
	}
	if strings.HasPrefix(cc, "+") && cc != "+86" || strings.EqualFold(cc, "+86") == false {
		// 国际短信
		params["Action"] = "SendInternationalSms"
		params["CountryCode"] = strings.TrimPrefix(cc, "+")
		params["PhoneNumbers"] = phone
		params["SignName"] = signName
		params["TemplateCode"] = templateCode
		params["TemplateParam"] = fmt.Sprintf(`{"code":"%s"}`, code)
	} else {
		params["PhoneNumbers"] = phone
		params["SignName"] = signName
		params["TemplateCode"] = templateCode
		params["TemplateParam"] = fmt.Sprintf(`{"code":"%s"}`, code)
	}

	params["Signature"] = aliyunSign(secretKey, params)
	query := encodeParams(params)

	resp, err := httpGet("https://dysmsapi.aliyuncs.com/?" + query)
	if err != nil {
		return err
	}
	var r struct {
		Code string `json:"Code"`
		Msg  string `json:"Message"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return err
	}
	if r.Code != "OK" {
		return fmt.Errorf("aliyun sms error: %s %s", r.Code, r.Msg)
	}
	return nil
}

func aliyunSign(secret string, params map[string]string) string {
	if secret == "" {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(percentEncode(k))
		sb.WriteString("=")
		sb.WriteString(percentEncode(params[k]))
		sb.WriteString("&")
	}
	s := strings.TrimSuffix(sb.String(), "&")
	stringToSign := "GET&%2F&" + percentEncode(s)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func encodeParams(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(url.QueryEscape(k))
		sb.WriteString("=")
		sb.WriteString(url.QueryEscape(params[k]))
		sb.WriteString("&")
	}
	return strings.TrimSuffix(sb.String(), "&")
}

func percentEncode(s string) string {
	// 阿里云要求：/ 不转义，+ 转义为 %20
	r := url.QueryEscape(s)
	r = strings.ReplaceAll(r, "+", "%20")
	r = strings.ReplaceAll(r, "*", "%2A")
	r = strings.ReplaceAll(r, "%7E", "~")
	return r
}

// ============ SMTP 邮件 ============

func sendEmailCode(host string, port int, user, password, from, to, code string) error {
	if host == "" || user == "" {
		return fmt.Errorf("smtp not configured")
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	if from == "" {
		from = user
	}
	subject := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte("企业IM验证码")) + "?="
	body := fmt.Sprintf("您的验证码是：%s，5 分钟内有效。\r\nYour verification code: %s, valid for 5 minutes.\r\n", code, code)
	msg := "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body

	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, password, host)
	}
	if port == 465 {
		// SSL 直连
		return smtpSendSSL(addr, auth, from, []string{to}, []byte(msg))
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
}

func smtpSendSSL(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if err = c.StartTLS(nil); err != nil {
		return err
	}
	if a != nil {
		if err = c.Auth(a); err != nil {
			return err
		}
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, t := range to {
		if err = c.Rcpt(t); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(msg)
	if err != nil {
		return err
	}
	return w.Close()
}

// 依赖注入点：便于后续替换/测试
var (
	httpGet = func(u string) ([]byte, error) {
		resp, err := getHTTP(u)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	timeNowISO = func() string { return timeNow().UTC().Format("2006-01-02T15:04:05Z") }
	nonce      = func() int64 { return timeNow().UnixNano() }
)

var timeNow = time.Now

func getHTTP(u string) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Get(u)
}
