package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const FastMaxPayload = 26214400
const FastAPIToken = "YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm"
const FastAPIURL = "https://api.fast.com/netflix/speedtest/v2?https=true&token=" + FastAPIToken

var tr = &http.Transport{
	TLSClientConfig:    &tls.Config{InsecureSkipVerify: true},
	DisableCompression: true,
	Proxy:              nil,
}

var client = &http.Client{Transport: tr}

type LocationInfo struct {
	City    string
	Country string
}

type ConnectionInfo struct {
	ASN      string
	IP       string
	Location LocationInfo
}

type FastServer struct {
	City    string
	Country string
	URL     string
}

type FakeReader struct {
	ReadIndex int64
	MaxIndex  int64
}

func (c *FakeReader) Read(p []byte) (int, error) {
	if c.ReadIndex >= c.MaxIndex {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 0
	}
	n := len(p)
	c.ReadIndex += int64(n)
	return n, nil
}

func FormatFastURL(url string, rangeEnd int) string {
	return strings.Replace(url, "/speedtest?", "/speedtest/range/0-"+strconv.Itoa(rangeEnd)+"?", -1)
}

func FastGetServerList(urlsToTest int) (ConnectionInfo, []FastServer) {
	resp, err := client.Get(FastAPIURL + "&urlCount=" + strconv.Itoa(urlsToTest))
	if err != nil {
		panic("Error getting server list")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic("Fast.com API returned " + resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic("Error reading server list")
	}
	var jsonData map[string]interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		panic("Error parsing server list")
	}
	var connectionInfo ConnectionInfo
	var locationInfo LocationInfo
	var fastServer FastServer
	var fastServerList []FastServer
	var serverList = jsonData["targets"].([]interface{})
	for _, server := range serverList {
		serverData := server.(map[string]interface{})
		fastServer = FastServer{
			City:    serverData["location"].(map[string]interface{})["city"].(string),
			Country: serverData["location"].(map[string]interface{})["country"].(string),
			URL:     serverData["url"].(string),
		}
		fastServerList = append(fastServerList, fastServer)
	}
	connectionInfo.IP = jsonData["client"].(map[string]interface{})["ip"].(string)
	connectionInfo.ASN = jsonData["client"].(map[string]interface{})["asn"].(string)
	locationInfo.City = jsonData["client"].(map[string]interface{})["location"].(map[string]interface{})["city"].(string)
	locationInfo.Country = jsonData["client"].(map[string]interface{})["location"].(map[string]interface{})["country"].(string)
	connectionInfo.Location = locationInfo
	return connectionInfo, fastServerList
}

func GetHost(_url string) string {
	u, err := url.Parse(_url)
	if err != nil {
		panic("Error parsing URL")
	}
	return u.Host
}

func GetLatency(url string) time.Duration {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		panic("Error creating request")
	}
	var t1, t2 time.Time
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) {
			t1 = time.Now()
		},
		ConnectDone: func(_, _ string, _ error) {
			t2 = time.Now()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	_, err = tr.RoundTrip(req)
	if err != nil {
		panic("Error making request")
	}
	return t2.Sub(t1)
}

func GetDownloadSpeed(url string) float64 {
	resp, err := client.Get(FormatFastURL(url, FastMaxPayload))
	if err != nil {
		panic("Error getting download speed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic("Fast.com API returned " + resp.Status)
	}
	t1 := time.Now()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		panic("Error reading download speed")
	}
	return float64(FastMaxPayload) / time.Since(t1).Seconds()
}

func GetUploadSpeed(url string) float64 {
	counter := &FakeReader{
		ReadIndex: 0,
		MaxIndex:  FastMaxPayload,
	}
	req, err := http.NewRequest("POST", FormatFastURL(url, FastMaxPayload), counter)
	if err != nil {
		panic("Error creating request")
	}
	req.ContentLength = FastMaxPayload
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept-Encoding", "identity")

	t1 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		panic("Error doing request")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic("Fast.com API returned " + resp.Status)
	}
	return float64(FastMaxPayload) / time.Since(t1).Seconds()
}

func main() {
	connectionInfo, fastServerList := FastGetServerList(1)
	fmt.Println("Fast.com Speedtest")
	fmt.Println()
	fmt.Println("Connection Info:")
	fmt.Println("  IP: " + connectionInfo.IP)
	fmt.Println("  ASN: " + connectionInfo.ASN)
	fmt.Println("  Location: " + connectionInfo.Location.City + ", " + connectionInfo.Location.Country)
	fmt.Println()
	fmt.Println("Fast.com Servers:")
	for _, server := range fastServerList {
		fmt.Println("  Location: " + server.City + ", " + server.Country)
		fmt.Println("  URL: " + server.URL)
		fmt.Println()
	}

	fmt.Println("Latency:")
	for _, server := range fastServerList {
		latency := GetLatency(server.URL)
		fmt.Printf("  %s: %dms\n", GetHost(server.URL), latency.Milliseconds())
	}
	fmt.Println()

	fmt.Println("Download Speed:")
	for _, server := range fastServerList {
		downloadSpeed := GetDownloadSpeed(server.URL)
		//fmt.Printf("  %s: %0.3fMB/s\n", GetHost(server.URL), downloadSpeed/1024/1024)
		fmt.Printf("  %s: %0.3fMbit/s\n", GetHost(server.URL), downloadSpeed/125000)
	}
	fmt.Println()

	fmt.Println("Upload Speed:")
	for _, server := range fastServerList {
		uploadSpeed := GetUploadSpeed(server.URL)
		//fmt.Printf("  %s: %0.3fMB/s\n", GetHost(server.URL), uploadSpeed/1024/1024)
		fmt.Printf("  %s: %0.3fMbit/s\n", GetHost(server.URL), uploadSpeed/125000)
	}
}
