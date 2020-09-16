package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/joho/godotenv"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

type DenyIps struct {
	IpAddress    string
	FailedLogins int
	BanTime      int
}

type AllowedIps struct {
	IpAddress string
}

var _defaultAllowedIps = defaultAllowedIps()

func defaultAllowedIps() []AllowedIps {
	allowedIps := `[{
		"ipAddress": "127.0.0.1"
		}, {
		"ipAddress": "192.168.7.100"
	}]`
	var allowList []AllowedIps
	err := json.Unmarshal([]byte(allowedIps), &allowList)
	if err != nil {
		log.Fatal(err)
	}
	return allowList
}

type Users struct {
	name string
	uuid string
}

var loadEnvVars sync.Once
var _defaultUsers = defaultUsers()

func defaultUsers() []Users {
	loadEnvVars.Do(func() {
		err := godotenv.Load("../.env")
		if err != nil {
			log.Fatal("Error loading dot env file!")
		}
	})
	user := Users{name: os.Getenv("AUTHORIZED_USER"), uuid: os.Getenv("AUTHORIZED_USER_KEY")}
	var usersList []Users
	usersList = append(usersList, user)

	return usersList
}

type MikrotikMessage struct {
	Message string
}

type webhookReqBody struct {
	Message struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

type sendMessageReqBody struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

var denyIpsList []DenyIps
var apiVersion = "0.0.7"

func getVersion() string {
	return fmt.Sprintf("API version %s", apiVersion)
}

func isUserIdAuthorized(users []Users, uuid string) bool {
	for _, user := range users {
		if user.uuid == uuid {
			return true
		}
	}
	return false
}

func telegramSendMessage(chatID int64, message string) error {
	reqBody := &sendMessageReqBody{
		ChatID: chatID,
		Text:   message,
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	// send msg with token
	res, err := http.Post(fmt.Sprintf("%s/sendMessage", os.Getenv("TELEGRAM_BASE_URL")), "application/json", bytes.NewBuffer(reqBytes))
	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return errors.New("unexpected status" + res.Status)
	}
	return nil
}

func denyListToString() string {
	var tmpArray []string
	if len(denyIpsList) > 0 {
		for _, item := range denyIpsList {
			fullLog := fmt.Sprintf("IP: %s had %s failed attempets and is blocked for %s minutes.", item.IpAddress, strconv.Itoa(item.FailedLogins), strconv.Itoa(item.BanTime))
			tmpArray = append(tmpArray, fullLog)
		}
		return strings.Join(tmpArray, "\n")
	}
	return "There are no blocked IPs!"
}

func index(w http.ResponseWriter, r *http.Request) {
	log.Printf(getVersion())
	w.WriteHeader(200)
	fmt.Fprintf(w, getVersion())
}

func telegram(w http.ResponseWriter, r *http.Request) {
	// decode the JSON response body
	body := &webhookReqBody{}
	if err := json.NewDecoder(r.Body).Decode(body); err != nil {
		fmt.Println("could not decode request body", err)
		return
	}

	// bot commands
	if strings.Contains(strings.ToLower(body.Message.Text), "/list") {
		blockedIps := denyListToString()
		err := telegramSendMessage(body.Message.Chat.ID, blockedIps)
		if err != nil {
			fmt.Println("error in sending reply:", err)
			return
		}
		return
	}

	if strings.Contains(strings.ToLower(body.Message.Text), "/version") {
		err := telegramSendMessage(body.Message.Chat.ID, getVersion())
		if err != nil {
			fmt.Println("Something went wrong to send the telegram message")
		}
		return
	}

	// log a confirmation message if the message is sent successfully
	fmt.Println("reply sent")
}

func calculateBan(ipsList []string) {
	// tmpArr := []DenyIps
	dict := make(map[string]int)
	for _, ip := range ipsList {
		addIt := true
		for _, allowedIp := range _defaultAllowedIps {
			if ip == allowedIp.IpAddress {
				addIt = false
			}
		}
		if addIt {
			dict[ip] = dict[ip] + 1
		}
	}

	for k, v := range dict {
		var deniedIp DenyIps
		if v > 7 {
			fmt.Println("ban this guy for a day!")
			// ip/failed login attempts/ban time
			deniedIp = DenyIps{k, v, 1440}
		} else if v >= 6 {
			fmt.Println("ban this guy for 30min")
			// ip/failed login attempts/ban time
			deniedIp = DenyIps{k, v, 30}
		} else if v >= 3 && v < 6 {
			fmt.Println("ban this guy for 5min")
			// ip/failed login attempts/ban time
			deniedIp = DenyIps{k, v, 5}
		}

		if deniedIp.BanTime > 0 {
			fmt.Printf("Baning IP %s for %v\n minutes!", deniedIp.IpAddress, deniedIp.BanTime)
			denyIpsList = append(denyIpsList, deniedIp)
		}
	}

}

func ipAddressHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {

	case http.MethodPost:
		userId := r.Header.Get("userid")
		if userId != "" && isUserIdAuthorized(_defaultUsers, userId) {
			data, _ := ioutil.ReadAll(r.Body)
			r.Body.Close()

			var mikrotikMessage MikrotikMessage
			json.Unmarshal(data, &mikrotikMessage)
			fmt.Printf("msg comming: %s \n", mikrotikMessage.Message)

			denyIpsJSON := `[]`
			err := json.Unmarshal([]byte(denyIpsJSON), &denyIpsList)
			if err != nil {
				fmt.Println("Unable to decode JSON")
			}

			var tmpIpsList []string
			splitString := strings.Fields(mikrotikMessage.Message)
			for _, item := range splitString {
				ip := net.ParseIP(item)
				if ip == nil {
					continue
				}
				tmpIpsList = append(tmpIpsList, item)
			}

			calculateBan(tmpIpsList)

			log.Printf("New message coming from Mikrotik!")

			denyIpsJson, err := json.Marshal(denyIpsList)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// send telegram msg
			// chatId, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
			// if err != nil {
			// 	fmt.Println(err)
			// }
			// message := fmt.Sprintf("Report\n---\n %s", denyListToString())
			// err = telegramSendMessage(chatId, message)
			// if err != nil {
			// 	fmt.Println("error")
			// }

			w.Write(denyIpsJson)
			w.WriteHeader(200)
			return
		} else {
			log.Printf("Not authorized!")
			w.WriteHeader(401)
			return
		}

	case http.MethodGet:

		userId := r.Header.Get("userid")

		fmt.Println("---")
		fmt.Println(_defaultUsers)

		if userId != "" && isUserIdAuthorized(_defaultUsers, userId) {
			log.Printf("Authorized!")
			denyIpsJson, err := json.Marshal(denyIpsList)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			w.Write(denyIpsJson)
		} else {
			log.Printf("Not authorized!")
			w.WriteHeader(401)
			return
		}
	}
}

func handleRequests() {
	http.HandleFunc("/", index)
	http.HandleFunc("/ipaddress", ipAddressHandler)
	http.HandleFunc("/telegram", telegram)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("SERVER_PORT")), nil))
}

func main() {
	fmt.Println("API is running...")
	handleRequests()
}
