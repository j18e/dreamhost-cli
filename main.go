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
	"log"
	"net"
	"net/http"
	"time"
)

const (
	usage         = `Usage: dreamdns-update --token=<token> <record>`
	DREAMHOST_API = "https://api.dreamhost.com"
	WHATSMYIP_API = "http://myexternalip.com/raw"
)

var httpCli = http.Client{Timeout: time.Second * 5}

func main() {
	apiKey := flag.String("api.key", "", "dreamhost api key with dns rw permissions")
	dnsRecord := flag.String("dns.record", "", "dns A record to check/create/modify")
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("required flag -api.key")
	}
	if *dnsRecord == "" {
		log.Fatal("required flag -dns.record")
	}

	ip, err := getExternalIP(WHATSMYIP_API)
	if err != nil {
		log.Fatalf("checking our public IP address: %v", err)
	}
	fmt.Printf("our current public IP address: %s\n", ip)

	records, err := getRecords(*apiKey)
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
		fmt.Println("found record pointing at our public IP address. Exiting...")
		return
	}

	// record does not match, must remove
	fmt.Printf("removing record %s->%s...\n", match.Record, match.Value)
	if err := removeRecord(*apiKey, match.Record, match.Value); err != nil {
		log.Fatalf("failed: %v", err)
	}
	fmt.Println("success")

	fmt.Printf("adding record %s->%s...\n", *dnsRecord, ip)
	if err := addRecord(*apiKey, *dnsRecord, ip); err != nil {
		log.Fatalf("creating A record %s->%s: %v", *dnsRecord, ip, err)
	}
	fmt.Println("success")
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

func addRecord(token string, record string, address string) error {
	const url = "%s?format=json&cmd=dns-add_record&key=%s&type=A&record=%s&value=%s"
	var r Result

	resp, err := httpCli.Get(fmt.Sprintf(url, DREAMHOST_API, token, record, address))
	if err != nil {
		return fmt.Errorf("contacting api: %w", err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if err = json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}
	if r.Result != "success" {
		return fmt.Errorf("record creation failed: %v", r.Data)
	}

	return nil
}

func removeRecord(token string, record string, address string) error {
	const url = "%s?format=json&cmd=dns-remove_record&key=%s&type=A&record=%s&value=%s"
	var r Result

	resp, err := httpCli.Get(fmt.Sprintf(url, DREAMHOST_API, token, record, address))
	if err != nil {
		return fmt.Errorf("contacting api: %w", err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if err = json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}
	if r.Result != "success" {
		return fmt.Errorf("record removal failed: %s", r.Data)
	}
	return nil
}

func getRecords(token string) ([]*Record, error) {
	var r struct {
		Data   []*Record `json:"data"`
		Result string    `json:"result"`
		Reason string    `json:"reason"`
	}
	url := fmt.Sprintf("%s?format=json&cmd=dns-list_records&key=%s", DREAMHOST_API, token)

	resp, err := httpCli.Get(url)
	if err != nil {
		return r.Data, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return r.Data, err
	}

	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	if r.Result != "success" {
		return r.Data, errors.New(r.Reason)
	}

	return r.Data, nil
}

func getExternalIP(apiAddr string) (string, error) {
	resp, err := httpCli.Get(apiAddr)
	if err != nil {
		return "", fmt.Errorf("contacting api: %w", err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	ip := string(body)

	if a := net.ParseIP(ip).To4(); a == nil {
		return "", fmt.Errorf("invalid ip address: %v", a)
	}

	return ip, nil
}
