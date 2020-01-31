// Dreamhost DNS updater manages DNS host (A) records configured in Dreamhost,
// for the sake of a server being able to update its own public IP address.
// Will delete/overwrite any A records not pointing at the server's public IP.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	DREAMHOST_API = "https://api.dreamhost.com"
	WHATSMYIP_API = "https://myexternalip.com/raw"
)

func main() {
	apiKey := flag.String("api.key", "", "Dreamhost API token with permissions to change DNS records")
	dnsRecord := flag.String("dns.record", "", "DNS A record to update with our external IP")
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("required flag -api.key")
	}
	if *dnsRecord == "" {
		log.Fatal("required flag -dns.record")
	}

	cli := Client{
		Client:        http.Client{Timeout: 5 * time.Second},
		dreamhostAddr: DREAMHOST_API,
		dreamhostTok:  *apiKey,
		extIPAddr:     WHATSMYIP_API,
	}

	ip, err := cli.ExtIP()
	if err != nil {
		log.Fatalf("checking our public IP address: %v", err)
	}
	log.Infof("our current public IP address: %s", ip)

	records, err := cli.Records()
	if err != nil {
		log.Fatalf("getting dns records from dreamhost: %v", err)
	}

	var match *Record
	for _, r := range records {
		if (r.Type == "A") && (r.Record == *dnsRecord) {
			match = r
			break
		}
	}
	if match == nil {
		log.Fatalf("no A records matching %s. Exiting...", *dnsRecord)
	}

	// do nothing if record matches our public ip
	if match.Value == ip {
		log.Info("found record pointing at our public IP address. Exiting...")
		return
	}

	// record does not match, must remove
	log.Infof("removing record %s->%s...", match.Record, match.Value)
	if err := cli.Delete(match.Record, match.Value); err != nil {
		log.Fatalf("failed: %v", err)
	}

	log.Infof("record removed. Creating record %s->%s...", *dnsRecord, ip)
	if err := cli.Create(*dnsRecord, ip); err != nil {
		log.Fatalf("creating A record %s->%s: %v", *dnsRecord, ip, err)
	}
	log.Info("record created")
}

type Record struct {
	Type   string `json:"type"`
	Record string `json:"record"`
	Value  string `json:"value"`
}

type Result struct {
	Result string `json:"result"`
	Data   string `json:"data"`
}

type Client struct {
	http.Client
	dreamhostAddr string
	dreamhostTok  string
	extIPAddr     string
}

func (c Client) Create(record string, address string) error {
	const url = "%s?format=json&cmd=dns-add_record&key=%s&type=A&record=%s&value=%s"

	res, err := c.Get(fmt.Sprintf(url, c.dreamhostAddr, c.dreamhostTok, record, address))
	if err != nil {
		return fmt.Errorf("contacting api: %w", err)
	}
	defer res.Body.Close()

	var r Result
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}

	if r.Result != "success" {
		return fmt.Errorf("record creation failed with %s: %s", r.Result, r.Data)
	}

	return nil
}

func (c Client) Delete(record string, address string) error {
	url := fmt.Sprintf("%s?format=json&cmd=dns-remove_record&key=%s&type=A&record=%s&value=%s",
		c.dreamhostAddr, c.dreamhostTok, record, address)

	res, err := c.Get(url)
	if err != nil {
		return fmt.Errorf("contacting api: %w", err)
	}
	defer res.Body.Close()

	var r Result
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}

	if r.Result != "success" {
		return fmt.Errorf("record removal failed with %s: %s", r.Result, r.Data)
	}
	return nil
}

func (c Client) Records() ([]*Record, error) {
	var r struct {
		Data   json.RawMessage `json:"data"`
		Result string          `json:"result"`
		Reason string          `json:"reason"`
	}
	url := fmt.Sprintf("%s?format=json&cmd=dns-list_records&key=%s", c.dreamhostAddr, c.dreamhostTok)

	res, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("unmarshaling json: %w", err)
	}

	if r.Result != "success" {
		return nil, errors.New(r.Reason)
	}

	var records []*Record
	if err := json.Unmarshal(r.Data, &records); err != nil {
		return nil, fmt.Errorf("unmarshalling json records: %w", err)
	}

	return records, nil
}

func (c Client) ExtIP() (string, error) {
	res, err := c.Get(c.extIPAddr)
	if err != nil {
		return "", fmt.Errorf("contacting my-external-ip api: %w", err)
	}
	defer res.Body.Close()

	bs, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	ip := net.ParseIP(string(bs))
	if ip == nil {
		return "", fmt.Errorf("invalid ip address: %s", ip)
	}

	return ip.String(), nil
}
