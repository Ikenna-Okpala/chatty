package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
)

const (
	listHeight = 14
	gap = "\n\n"
)

var (
	titleStyle = lipgloss.NewStyle().MarginLeft(2)
	itemStyle = lipgloss.NewStyle().PaddingLeft(4)
	paginationStyle = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)
	quitStyle = lipgloss.NewStyle().Margin(1, 0, 2, 4)
)


type Friend string

func (f Friend) FilterValue() string {return ""}

type FriendDelegate struct{
	Theme int
}

func (f FriendDelegate) Height() int{return 1}

func (f FriendDelegate) Spacing() int {return 0}

func (f FriendDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd {return nil}

func (f FriendDelegate) Render(w io.Writer, m list.Model, index int, listFriend list.Item){

	friend, ok:= listFriend.(Friend)

	if !ok {
		return
	}

	str:= fmt.Sprintf("%d. %s", index + 1, friend)

	fn:= itemStyle.Render

	if m.Index() == index {
		fn = func(strs ...string) string {

			selectedItemStyle := lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color(strconv.Itoa(f.Theme)))
			return selectedItemStyle.Render("> "+ strings.Join(strs, " "))
		}
	}

	fmt.Fprint(w, fn(str))
}

type TypeInfo struct{
	Index int
	Color int
	From string
}

type Model struct {
	List list.Model
	Friend Friend
	ExitMessage string
	Input textinput.Model
	WhoAmI string
	Conn * websocket.Conn
	Spinner spinner.Model
	CurrWindow int
	ViewPort viewport.Model
	TextArea textarea.Model
	Messages [] string
	RecvChan chan MessageRecvMsg
	Theme int
	TypingCtx context.Context
	TypingCancelFunc context.CancelFunc
	PointsSpinner spinner.Model
	EventTracking map[string] *TypeInfo
	connMutex sync.Mutex
}

type ErrorMsg struct{err error}


func (e ErrorMsg) Error() string{

	return e.err.Error()
}

func InitColor() int {

	blacklist:= [] int{
		8,
		15,
		16,
	}


	color:= rand.IntN(229) + 1

	for slices.Contains(blacklist, color) {
		color = rand.IntN(229) + 1
	}


	return color
}

func InitialModel() * Model {

	ta:= textarea.New()

	ta.Placeholder = "Send a message..."

	ta.Focus()

	ta.Prompt = "┃"

	ta.CharLimit = 280

	ta.SetWidth(30)

	ta.SetHeight(3)

	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ta.ShowLineNumbers = false

	vp:= viewport.New(30, 5)

	vp.SetContent("Welcome to the chat room! Type a message and press Enter to send.")

	ta.KeyMap.InsertNewline.SetEnabled(false)

	const defaultWidth = 20

	color:= InitColor()

	l:= list.New([]list.Item{}, FriendDelegate{
		Theme: color,
	}, defaultWidth, listHeight)

	l.Title = "Who do you want to chat with?"

	l.SetShowStatusBar(false)

	l.SetFilteringEnabled(false)

	l.Styles.Title = titleStyle

	l.Styles.PaginationStyle = paginationStyle

	l.Styles.HelpStyle = helpStyle


	ti:= textinput.New()

	ti.Placeholder = "Username:"

	ti.Focus()

	ti.CharLimit = 156

	ti.Width = 20

	s:= spinner.New()

	ellipsis:= spinner.New()

	ellipsis.Spinner = spinner.Ellipsis

	s.Spinner = spinner.Line

	

	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("%d", color)))

	typingCtx, typingCancelFuc:= context.WithCancel(context.Background())

	return &Model{
		Input: ti,
		Spinner: s,
		List: l,
		TextArea: ta,
		ViewPort: vp,
		RecvChan: make(chan MessageRecvMsg),
		Theme: color,
		TypingCtx: typingCtx,
		TypingCancelFunc: typingCancelFuc,
		PointsSpinner: ellipsis,
		EventTracking: make(map[string]*TypeInfo),

	}
}

type ConnMsg struct{
	Conn *websocket.Conn
}

var HOST = ""


func Connect(whoAmI string) tea.Cmd {

		return func() tea.Msg {

			if HOST == ""{
				return ErrorMsg{err: errors.New("Env Failure")}
			}

			url:= url.URL{
				Scheme: "wss",
				Host: HOST,
				Path: "/chat/"+whoAmI,
			}
		
			c, _, err:= websocket.DefaultDialer.Dial(url.String(), nil)

		
			if err != nil {
				log.Println(err)
				return ErrorMsg{err: err}
			}

			//log.Println("Returning connection...")
		
			return ConnMsg{Conn: c}
		
		}
}


func (m * Model) Init () tea.Cmd {

	return tea.Batch(
		textinput.Blink,
		m.Spinner.Tick,
		textarea.Blink,
		m.PointsSpinner.Tick,
	)
}

type DoneMsg struct{}

func FinalWords(conn * websocket.Conn) tea.Cmd{


	if conn != nil {
		err:= conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

		if err != nil {
			//log.Println(err)
		}
	}
	
	return func() tea.Msg {
		time.Sleep(time.Second * 1)
		return DoneMsg{}
	}

	
}

type Message interface {
	Recv()
}

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

func (cm ChatMessage) Recv(){}


type FriendList [] Friend
func (friendList FriendList) Recv(){}


func (t TypingMessage) Recv(){}

type MessageSentMsg struct{}

type MessageRecvMsg struct{
	message Message
}

func SyncSend(mux * sync.Mutex, conn *websocket.Conn, messageWrapper MessageWrapper) error{
	mux.Lock()

	defer mux.Unlock()

	if err:= conn.WriteJSON(messageWrapper); err != nil {
		return err
	}

	return nil

}

func SendText(conn *websocket.Conn, mux * sync.Mutex, whoAmI string, friend Friend, text string, color int) tea.Cmd {

	message:= ChatMessage{
		To: string(friend),
		From:whoAmI,
		Text: text,
		Color: color,
	}

	return func() tea.Msg {

	raw, err:= json.Marshal(message)

	if err != nil {
		return ErrorMsg{err:err}
	}

	messageWrapper:= MessageWrapper{
		Type: "chat",
		Value: raw,
	}

	

	if err:= SyncSend(mux, conn, messageWrapper); err != nil {
		return ErrorMsg{err:err}
	}

	typingWrapper:= JsonTyping(false, string(friend), color, whoAmI)


	if err:= SyncSend(mux, conn, typingWrapper); err != nil {

		//log.Println(err)
	}


	//log.Printf("Sent message to friend:%s\n", friend)

	return MessageSentMsg{}

	}
}

func ShortLiveRecv (recvChan chan MessageRecvMsg) tea.Cmd{

	return func() tea.Msg {

		msg:= <- recvChan

		return msg
	}
}

func RecvMessage (conn  * websocket.Conn, recvChan chan MessageRecvMsg) tea.Cmd {

	return func() tea.Msg {

		for{
			msg:= new(MessageWrapper)

			_, incoming, err:= conn.ReadMessage()

			if err != nil {

				if websocket.IsCloseError(err, websocket.CloseNormalClosure){
					return nil
				}else{
					//log.Println(err)
					return ErrorMsg{err: err}
				}
				
			}

			//log.Println("Received text: ", string(incoming))

			if err:= json.Unmarshal(incoming, msg); err!= nil {
				return ErrorMsg{err: err}
			}

			chatMessage:= new(ChatMessage)
			typingStatus:= new(TypingMessage)
			friendsMesage:= new(FriendList)

			var message Message

			go func(){

				// log.Println("Processing received message")

				switch msg.Type {
				case "chat":
					err:= json.Unmarshal(msg.Value, chatMessage)

					if err != nil {
						fmt.Println(err)
					}
					message = *chatMessage

				case "typing":
					err:= json.Unmarshal(msg.Value, typingStatus)

					if err != nil {
						fmt.Println(err)
					}
					message = *typingStatus

				case "friends":
					err:= json.Unmarshal(msg.Value, friendsMesage)

					if err != nil {
						fmt.Println(err)
					}

					message = *friendsMesage

				}
				recvChan <-  MessageRecvMsg{
					message: message,
				}
			}()
		}


	}
}

func TimeStamp() string{

	time:= time.Now()

	return fmt.Sprintf("[%d:%d]", time.Hour(), time.Minute())

}

func FriendsToItems(friends [] Friend) [] list.Item {
	items:= make([] list.Item, len(friends))
	for i, ele:= range friends{
		items[i] = ele
	}

	return items
}

func JsonTyping(isTyping bool, to string, color int, from string) MessageWrapper{
	typingMessage:= TypingMessage{
		IsTyping: isTyping,
		To: to,
		Color: color,
		From: from,
	}

	raw, err:= json.Marshal(typingMessage)

	if err != nil {
		fmt.Println(err)
	}

	return MessageWrapper{
		Type: "typing",
		Value: raw,
	}

}

func TypingObserver(ctx context.Context, conn * websocket.Conn, mux * sync.Mutex, to string, color int, from string) tea.Cmd {

	return func() tea.Msg {

		//log.Println("Sending type info...")

		typingWrapper:= JsonTyping(true, to, color, from)

		if err:= SyncSend(mux, conn, typingWrapper); err != nil {
			//log.Println(err)
		}
		

		delay:= time.NewTimer(time.Second * 3)
		select {

		case <- delay.C:
			//log.Println("Terminating typing...")

			typingWrapper:= JsonTyping(false, to, color, from)
			if err:= SyncSend(mux, conn, typingWrapper); err != nil {

				//log.Println(err)
			}
		case <- ctx.Done():

			//log.Println("Terminating context...")
			return nil
		}

		return nil
	}
}


func BlackListTypingKeys() [] tea.KeyType{

	return []  tea.KeyType{
		tea.KeyEnter,
		tea.KeyCtrlC,
		tea.KeyType(tea.MouseButtonWheelDown),
		tea.KeyType(tea.MouseButtonWheelDown),
	}
}

func (m * Model) Update(msg tea.Msg) (tea.Model, tea.Cmd){
	var cmd tea.Cmd

	switch msgT:= msg.(type) {

	case tea.WindowSizeMsg:
		m.List.SetWidth(msgT.Width)
		m.ViewPort.Width = msgT.Width
		m.TextArea.SetWidth(msgT.Width)

		m.ViewPort.Height = msgT.Height - m.TextArea.Height() - lipgloss.Height(gap)

		if len(m.Messages) > 0{
			m.ViewPort.SetContent(lipgloss.NewStyle().Width(m.ViewPort.Width).Render(strings.Join(m.Messages, gap)))
		}

		m.ViewPort.GotoBottom()
		return m, nil

	case tea.KeyMsg:

		if m.CurrWindow == 3 && !slices.Contains(BlackListTypingKeys(), msgT.Type){
			m.TypingCancelFunc()

			var(
				tiCmd tea.Cmd
				vpCmd tea.Cmd
			)

			m.TextArea, tiCmd = m.TextArea.Update(msg)
			m.ViewPort, vpCmd = m.ViewPort.Update(msg)

			ctx, cancel:= context.WithCancel(context.Background())

			m.TypingCtx = ctx
			m.TypingCancelFunc = cancel
			
			
			return m, tea.Batch(TypingObserver(m.TypingCtx, m.Conn, &m.connMutex, string(m.Friend), m.Theme, m.WhoAmI,), tiCmd, vpCmd)


		}

		switch msgT.Type {

		case tea.KeyCtrlC, tea.KeyEsc:
			m.ExitMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("Goodbye!!!")
			return m, FinalWords(m.Conn)

		
		case tea.KeyUp, tea.KeyPgUp:
			m.ViewPort.ScrollUp(1)

		
		case tea.KeyDown, tea.KeyPgDown:
			m.ViewPort.ScrollDown(1)

		case tea.KeyEnter:

			if m.CurrWindow == 0{
				m.WhoAmI = m.Input.Value()
				m.CurrWindow = 1
				return m, Connect(m.WhoAmI)
			} else if m.CurrWindow == 2{

				friend, ok:= m.List.SelectedItem().(Friend)

				//log.Println("Selected Friend: ", friend)

				if ok {
					m.Friend = friend
					m.CurrWindow = 3
					m.TextArea.Reset()


					
						m.ViewPort.Style = lipgloss.NewStyle().
						BorderStyle(lipgloss.RoundedBorder()).
						BorderForeground(lipgloss.
							Color(strconv.Itoa(m.Theme))).Padding(2)
					
						m.TextArea.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(m.Theme)))

						m.TextArea.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(m.Theme))).Render("┃")

				}else{
					//log.Println("Cannot select friend")
				}

				return m, nil
			} else if m.CurrWindow == 3 {

				if m.TextArea.Value() > ""{

					return m, SendText(m.Conn, &m.connMutex, m.WhoAmI, m.Friend, m.TextArea.Value(), m.Theme)

				}
				
			}
			
		}

	case spinner.TickMsg:
		
		var (
			cmd1 tea.Cmd
			cmd2 tea.Cmd
		)
		m.Spinner, cmd1 = m.Spinner.Update(msgT)
		m.PointsSpinner, cmd2 = m.PointsSpinner.Update(msgT)

		for _, typing:= range m.EventTracking{

			receiverStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(typing.Color)))
			m.Messages[typing.Index] = receiverStyle.Render(fmt.Sprintf("%s %s: %s", TimeStamp(), typing.From, m.PointsSpinner.View()))
		}

		m.ViewPort.SetContent(lipgloss.NewStyle().Width(m.ViewPort.Width).Render(strings.Join(m.Messages, gap)))
		m.ViewPort.GotoBottom()

		return m, tea.Batch(cmd1, cmd2)

	
	case ErrorMsg:
		m.ExitMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(msgT.Error())
		//log.Println(m.ExitMessage)
		return m, FinalWords(m.Conn)

	case DoneMsg:
		return m, tea.Quit
		
	
	case ConnMsg:
		m.CurrWindow = 2
		m.Conn = msgT.Conn
		return m, tea.Batch(
		tea.Batch(RecvMessage(m.Conn, m.RecvChan),
		ShortLiveRecv(m.RecvChan),
		))
	
	
	case MessageSentMsg:

		senderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(m.Theme)))

		m.Messages = append(m.Messages, senderStyle.Render(fmt.Sprintf("%s You: %s", TimeStamp(), m.TextArea.Value())))
		m.ViewPort.SetContent(lipgloss.NewStyle().Width(m.ViewPort.Width).Render(strings.Join(m.Messages, gap)))
		m.TextArea.Reset()
		m.ViewPort.GotoBottom()

		return m, nil

	
	case MessageRecvMsg:

		//log.Println("Just received msg....")

		switch event:= msgT.message.(type){

		case ChatMessage:
			receiverStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(event.Color)))

			
			m.Messages = append(m.Messages,
				receiverStyle.Render(
					fmt.Sprintf("%s %s: %s", TimeStamp(), event.From, event.Text),
					))
			m.ViewPort.SetContent(lipgloss.NewStyle().Width(m.ViewPort.Width).Render(strings.Join(m.Messages, gap)))
			m.ViewPort.GotoBottom()
	
			return m, ShortLiveRecv(m.RecvChan)
		
		case FriendList:
			m.List.SetItems(FriendsToItems(event))
			return m, ShortLiveRecv(m.RecvChan)

		
		case TypingMessage:
			if event.IsTyping{

				if _, ok:= m.EventTracking[event.From]; !ok {
					m.EventTracking[event.From] = &TypeInfo{
						Index: len(m.Messages),
						Color: event.Color,
						From: event.From,
					}
	
					m.Messages = append(m.Messages, fmt.Sprintf("%s %s: %s", TimeStamp(), event.From, m.PointsSpinner.View()))
				}
					
			}else{
				
				typing, ok:= m.EventTracking[event.From]

				if ok{
					m.Messages = slices.Delete(m.Messages, typing.Index, typing.Index+1)
					delete(m.EventTracking, event.From)
	
					//reset indexes
	
					for _, otherTyping:= range m.EventTracking{
						if otherTyping.Index >= typing.Index{
							otherTyping.Index--
						}
					}
				}

				
			}

			m.ViewPort.SetContent(lipgloss.NewStyle().Width(m.ViewPort.Width).Render(strings.Join(m.Messages, gap)))
			m.ViewPort.GotoBottom()
			
			return m, ShortLiveRecv(m.RecvChan)
		
		}

	}

	

	m.Input, cmd = m.Input.Update(msg)
	m.List, cmd = m.List.Update(msg)

	var(
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.TextArea, tiCmd = m.TextArea.Update(msg)
	m.ViewPort, vpCmd = m.ViewPort.Update(msg)

	return m, tea.Batch(
		cmd,
		tiCmd,
		vpCmd,
	)
}

func (m * Model) View() string {

	str:= "\n"

	if m.ExitMessage == ""{
		if m.CurrWindow == 0{

			str+= lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(m.Theme))).Render(m.Input.View())
	
		}else if m.CurrWindow == 1{
			str+=fmt.Sprintf("%s Connecting to server...\n\n", m.Spinner.View())
		}else if m.CurrWindow == 2{
	
			if len(m.List.Items()) <= 0 {
				str+=lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(m.Theme))).Render(fmt.Sprintf("%s Waiting for users... \n\n", m.Spinner.View()))
			}else {
				str+= m.List.View()
			}
	
			
		}else if m.CurrWindow == 3{

			if len(m.Messages) == 0{
				style:= lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(m.Theme)))

					display:= fmt.Sprintf("Welcome to the %s's dm! Type a message and press Enter to send.", m.Friend)
					m.ViewPort.SetContent(style.Render(display))
			}
			str += fmt.Sprintf("%s%s%s", 
			m.ViewPort.View(),
			gap,
			m.TextArea.View(),
		)

		}
	}else {
		str+= m.ExitMessage
	}
	


	return str
	
}

func InitLogger(){


	files, err2:= os.ReadDir(".")

	if err2 != nil {
		panic(err2)
	}

	for _, file:= range files{

		if strings.HasPrefix(file.Name(), "chat"){
			os.Remove(file.Name())
		}
	}

	fName:= fmt.Sprintf("chat-%d.log", rand.IntN(100))
	file, err:= os.Create(fName)

	if err != nil {
		panic(err)
	}

	log.SetOutput(file)
}

func main(){

	//InitLogger()

	// if err:= godotenv.Load(".env"); err!= nil{
	// 	panic(err)
	// }	

	var model tea.Model = InitialModel()

	p:= tea.NewProgram(model, tea.WithAltScreen())

	if _, err:= p.Run(); err != nil {
		//log.Println("Something went wrong")
	}
}
