package tgsrv

const (
	UITypeButton = "button"
)

const (
	UIStyleSuccess = "success"
	UIStyleDanger  = "danger"
	UIStyleDefault = "default"
	UIStylePrimary = "primary"
)

/*
По умолчанию ответ на команду видит только тот, кто её ввёл (Ephemeral). Если вы хотите, чтобы вопрос и результат были видны всем в канале, добавьте в корень JSON параметр "response_type": "in_channel"
*/

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
	Context MUIContext `json:"context"`
}

type MUIContext struct {
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
	Context    MUIContext `json:"context"`
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
