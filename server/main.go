package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

type MessageWrapper struct{
	Type string `json:"type"`
	Value json.RawMessage `json:"value"`
}


type ChatMessage struct {
	To string `json:"to"`
	From string `json:"from"`
	Text string `json:"text"`
	Color int `json:"color"`
}

type TypingMessage struct {
	IsTyping bool `json:"isTyping"`
	To string `json:"to"`
	Color int `json:"color"`
	From string `json:"from"`
}

type Friend string
type FriendList [] Friend


func FailOnEnv(env string){

	if env == ""{
		panic("Env failure")
	}
}

func Redis() * redis.Client{
	redisServer:= os.Getenv("REDIS_INSTANCE")

	FailOnEnv(redisServer)

	redisUser:= os.Getenv("REDIS_USERNAME")

	FailOnEnv(redisUser)

	redisPassword:= os.Getenv("REDIS_PASSWORD")

	FailOnEnv(redisPassword)

	redisDb:= os.Getenv("REDIS_DB")

	FailOnEnv(redisDb)

	db, err:= strconv.Atoi(redisDb)

	if err!= nil {
		panic(err)
	}

	return redis.NewClient(
		&redis.Options{
			Addr: redisServer,
			Username: redisUser,
			Password: redisPassword,
			DB: db,

		},
	)
}

var upgrader = websocket.Upgrader{

	CheckOrigin: func(r *http.Request) bool {return true},

}

type WsServer struct{
	Redis *redis.Client
}

func (ws * WsServer)Chat(w  http.ResponseWriter, r * http.Request){

	id:= r.PathValue("id")

	if id == ""{
		http.Error(w, "No id", http.StatusBadRequest)
		return
	}

	conn, err:= upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)
	}

	ctx:= context.Background()

	sub:= ws.Redis.Subscribe(ctx, id)

	allSub:= ws.Redis.Subscribe(ctx, "all")

	_, err2:= allSub.Receive(ctx)

	if err2 != nil {
		fmt.Println(err2)
		return
	}

	allCh:= allSub.Channel()

	go func(){
		for event:= range allCh{

			fmt.Println(id, "joined....")

			active:= make([] string, 0)

			if err:= json.Unmarshal([]byte(event.Payload), &active); err != nil {
				fmt.Println(err)
			}

			active = slices.DeleteFunc(active, func(ele string) bool {
				return id == ele
			})


			raw, err:= json.Marshal(active)

			if err != nil {
				fmt.Println(err)
			}

			messageWrapper:= MessageWrapper{
				Type: "friends",
				Value: raw,
			}


			// fmt.Println("Friends Update: ", string(msg))


			if err:= conn.WriteJSON(messageWrapper); err!= nil {
				fmt.Println(err)

			}
		}
	}()


	active, err:= ws.AllActiveUsers()

	if err != nil {
		fmt.Println(err)
	}

	active = append(active, id)

	activeEnc, err2:= json.Marshal(active)

	if err2 != nil {
		fmt.Println(err)
	}


	if err:= ws.Redis.Publish(ctx, "all", activeEnc).Err(); err != nil {
		fmt.Println(err)
	}


	ch:= sub.Channel()

	if err:= ws.Redis.SAdd(ctx, "active:channels", id).Err(); err != nil{
		fmt.Println(err)
	}

	

	defer conn.Close()

	fmt.Println(id, "is active")

	go func(){

		defer sub.Close()
		defer allSub.Close()

		for {
			
			messageWraper:= new(MessageWrapper)

			 _, msg, err:= conn.ReadMessage()

			 fmt.Println("Received message from", id)

			 if err != nil {
				
				if websocket.IsCloseError(err, websocket.CloseNormalClosure){
					fmt.Println(id, "left")
					return
				}else{
					fmt.Println(err)
					return
				}
			 }

			 

			 if err:= json.Unmarshal(msg, messageWraper); err != nil {
				fmt.Println(err)
			 }

			 chatting:= new(ChatMessage)
			 typing:= new(TypingMessage)

			 switch messageWraper.Type {

			 case "chat":

				if err:= json.Unmarshal(messageWraper.Value, chatting); err != nil{
					fmt.Println(err)
				}

				if err:= ws.Redis.Publish(ctx, chatting.To, string(msg)).Err(); err != nil {
					fmt.Println(err)
					break
				}

			case "typing":
				if err:= json.Unmarshal(messageWraper.Value, typing); err != nil{
					fmt.Println(err)
				}

				if err:= ws.Redis.Publish(ctx, typing.To, string(msg)).Err(); err != nil {
					fmt.Println(err)
					break
				}
			 }
		}
	}()


	for incoming:= range ch {

		fmt.Println("Sending chat: ", incoming.Payload)
		if err:= conn.WriteMessage(websocket.TextMessage, []byte(incoming.Payload)); err != nil {
			fmt.Println(err)
				break
		}
	}

	if err:= ws.Redis.SRem(ctx, "active:channels", id).Err(); err != nil {
		fmt.Println(err)
	}

}

func (ws * WsServer) AllActiveUsers() ([] string, error){
	online:= make([]string, 0)

	ctx:= context.Background()

	active, err:= ws.Redis.SMembers(ctx, "active:channels").Result()
	if err != nil {
		
		return nil, err
	}

	for _, user:= range active{
		online = append(online, user)
	}

	return online, nil
}

func (ws * WsServer) Health(w http.ResponseWriter, r * http.Request){

	w.WriteHeader(http.StatusOK)

	_, err:= io.WriteString(w, "Healthy")

	if err != nil {
		fmt.Println(err)
	}
}

func main() {


	if err:= godotenv.Load(".env"); err!= nil {
		fmt.Println(err)
	}

	server:= WsServer{
		Redis: Redis(),
	}

	http.HandleFunc("/chat/{id}", server.Chat)
	http.HandleFunc("/health", server.Health)


	http.ListenAndServe(":8080", nil)
}