package tgsrv

import "fmt"

const (
	UITypeButton = "button"
)

const (
	UIStyleSuccess = "success"
	UIStyleDanger  = "danger"
	UIStyleDefault = "default"
	UIStylePrimary = "primary"

	UIActionOpen = "open"
)

/*
По умолчанию ответ на команду видит только тот, кто её ввёл (Ephemeral). 
Если вы хотите, чтобы вопрос и результат были видны всем в канале, 
добавьте в корень JSON параметр "response_type": "in_channel"
*/

/*
	"props": {
	    "test_data": {
	        "ios": 78,
	        "server": 948,
	        "web": 123
	    }
	},
*/
type MattermostResponse struct {
	ResponseType   string `json:"response_type"` // "in_channel"
	Text           string `json:"text"`
	Username       string `json:"username"` // anjella
	IconUrl        string `json:"icon_url"` // https://7slavka.ru/images/anjella.png
	ExtraResponses []struct {
		Text     string `json:"text"`
		Username string `json:"username"` // anjella
	} `json:"company_id"`
}

func NewMattermostResponse(text string) *MattermostResponse {
	return &MattermostResponse{
		// ResponseType: "in_channel", так ответ увидят все
		Text:         text,
		Username:     mattermostCommandResponseUsername,
		IconUrl:      mattermostCommandResponseIconUrl,
	}
}

type MattermostRequest struct {
	ChannelId   string `schema:"channel_id"`   // 7u35jijrnjfsixujqdg9ifmt4o
	ChannelName string `schema:"channel_name"` // town-square
	Command     string `schema:"command"`      // /7_totp_auth
	ResponseUrl string `schema:"response_url"` // https://mattermost.7slavka.ru/hooks/commands/nq4n1ha3fbny5krpy9zwty4n4o
	TeamDomain  string `schema:"team_domain"`  // snt-semislavka
	TeamId      string `schema:"team_id"`      // 838fra6nsi8tzfgnscwg98679a
	Text        string `schema:"text"`
	Token       string `schema:"token"`
	TriggerId   string `schema:"trigger_id"` // ZDM2c3o2dzRnM243aWtwM2NiZXRzcHN6NGg6cmRtejlyamF5dHJ0M2c3NGp0ZWY3NW9iZnI6MTc3Njc1NjYxNDk0MzpNRVFDSUdFTHVJNVZZenAveUhkV2ZRMklTYVdiSVcrdnh3ZDh5Zi9yS01aNjZVQjNBaUJxWGZabmdWbDJuWVcxbFFMQURNbGIvWFkxeHNUS1A5bW84MWZBQ3FJbGtnPT0%3D
	UserId      string `schema:"user_id"`    // rdmz9rjaytrt3g74jtef75obfr
	UserName    string `schema:"user_name"`  // michael
}

func (r *MattermostRequest) systemBotDirectMessage() bool {
	const template = "%s__%s"
	return r.ChannelName == fmt.Sprintf(template, systemBotId, r.UserId) ||
		r.ChannelName == fmt.Sprintf(template, r.UserId, systemBotId)
}

type MattermostUIResponse struct {
	Attachments []*MattermostUIAttachment `json:"attachments"`
}

type MattermostUIAttachment struct {
	Text    string                `json:"text"`
	Actions []*MattermostUIAction `json:"actions"`
}

type MattermostUIAction struct {
	Id          string          `json:"id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Style       string          `json:"style"`
	Integration MMUIIntegration `json:"integration"`
}

type MMUIIntegration struct {
	Url     string     `json:"url"`
	Context MMUIContext `json:"context"`
}

type MMUIContext struct {
	Action string `json:"action"`
	Value  bool   `json:"value"`
}

func (a *MattermostUIAttachment) addAction(p *MattermostUIAction) {
	a.Actions = append(a.Actions, p)
}

type MattermostActionRequest struct {
	UserId     string     `json:"user_id"`
	ChannelId  string     `json:"channel_id"`
	TeamId     string     `json:"team_id"`
	PostId     string     `json:"post_id"`
	TriggerId  string     `json:"trigger_id"`
	Type       string     `json:"type"`
	DataSource string     `json:"data_source"`
	Context    MMUIContext `json:"context"`
}

type MattermostActionResponse struct {
	Update struct {
		Message string `json:"message"`
	} `json:"update"`
}

func NewMattermostActionResponse(msg string) *MattermostActionResponse {
	resp := new(MattermostActionResponse)
	resp.Update.Message = msg
	return resp
}
