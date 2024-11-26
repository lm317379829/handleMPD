package main

import (
	"embed"
	"flag"
	"fmt"
	"time"
	"io"
	"os"
	"bytes"
	"net/http"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
)

//go:embed static/index.html
var indexHTML embed.FS

func handelMain(w http.ResponseWriter, req *http.Request) {
	if req.URL.RawQuery == "" {
		// 如果没有查询参数，则返回 index.html 的内容
		// 获取嵌入的 index.html 文件
		index, err := indexHTML.Open("static/index.html") // 假设静态文件路径是 static/index.html
		if err != nil {
			http.Error(w, "内部服务器错误", http.StatusInternalServerError)
			return
		}
		defer index.Close()

		// 将嵌入的文件内容复制到响应中
		_, err = io.Copy(w, index)
		if err != nil {
			log.Printf("写入 index.html 响应失败: %v", err)
		}
		return
	}

	query := req.URL.Query()
	mpdUrl := query.Get("mpdurl")
	proxyUrl := query.Get("proxyurl")
	if mpdUrl == "" || proxyUrl == "" {
		http.Error(w, "缺少 mpdurl 或 proxyurl 参数", http.StatusBadRequest)
		return
	}

	client := http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
		},
	}

	mpdReq, _ := http.NewRequest("GET", mpdUrl, nil)
	resp, err := client.Do(mpdReq)
	if err != nil {
		log.Printf("访问 %s 创建失败: %v", mpdUrl, err)
		http.Error(w, "访问目标地址失败", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 获取最终的 URL
	finalURL := resp.Request.URL.String()

	// 读取 MPD 内容
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("读取响应内容失败: %v", err)
		http.Error(w, "读取目标响应内容失败", http.StatusInternalServerError)
		return
	}
	mpdContent := string(body)

	// 提取 <BaseURL> 标签内容
	re := regexp.MustCompile(`<BaseURL>(.*?)</BaseURL>`)
	match := re.FindStringSubmatch(mpdContent)
	if len(match) < 2 {
		log.Println("MPD 中未找到 BaseURL")
		http.Error(w, "MPD 中未找到 BaseURL", http.StatusInternalServerError)
		return
	}
	originBaseUrl := match[1]

	// 替换 BaseURL
	var baseUrl string
	if strings.HasPrefix(originBaseUrl, "http") {
		baseUrl = fmt.Sprintf("%s%s", proxyUrl, originBaseUrl)
	} else {
		originUrl := finalURL[:strings.LastIndex(finalURL, "/")+1]
		baseUrl = fmt.Sprintf("%s%s%s", proxyUrl, originUrl, originBaseUrl)
	}
	newMpdContent := re.ReplaceAllString(mpdContent, fmt.Sprintf("<BaseURL>%s</BaseURL>", baseUrl))
	if newMpdContent == "" {
		log.Println("替换后的 MPD 内容为空")
		http.Error(w, "生成的 MPD 内容为空", http.StatusInternalServerError)
		return
	}

	// 设置响应头
	statusCode := resp.StatusCode
	responseHeaders := resp.Header
	for key, values := range responseHeaders {
		if strings.EqualFold(strings.ToLower(key), "connection") || strings.EqualFold(strings.ToLower(key), "content-disposition") || strings.EqualFold(strings.ToLower(key), "content-length") || strings.EqualFold(strings.ToLower(key), "proxy-connection") || strings.EqualFold(strings.ToLower(key), "transfer-encoding") {
			continue
		}
		w.Header().Set(key, strings.Join(values, ","))
	}

	// 处理文件名
	var fileName string
	contentDisposition := strings.ToLower(responseHeaders.Get("Content-Disposition"))
	if contentDisposition != "" {
		regCompile := regexp.MustCompile(`^.*filename=\"([^\"]+)\".*$`)
		if regCompile.MatchString(contentDisposition) {
			fileName = regCompile.ReplaceAllString(contentDisposition, "$1")
		}
	}
	if fileName == "" {
		queryIndex := strings.Index(finalURL, "?")
		if queryIndex == -1 {
			fileName = finalURL[strings.LastIndex(finalURL, "/")+1:]
		} else {
			fileName = finalURL[strings.LastIndex(finalURL[:queryIndex], "/")+1 : queryIndex]
		}
	}
	
	contentLength := len(newMpdContent)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", fileName))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
	w.Header().Set("Connection", "close")
	w.WriteHeader(statusCode)

	// 写入最终的 MPD 内容
	bodyReader := bytes.NewReader([]byte(newMpdContent))
	_, err = io.Copy(w, bodyReader)
	if err != nil {
		log.Printf("写入响应失败: %v", err)
	}
}

func main() {
	// 定义命令行参数
	port := flag.String("port", "10079", "端口")

	// 解析命令行参数
	flag.Parse()

	// 设置日志输出
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	s := http.Server{
		Addr:    ":" + *port,
		Handler: http.HandlerFunc(handelMain),
	}
	s.SetKeepAlivesEnabled(false)
	s.ListenAndServe()
}
