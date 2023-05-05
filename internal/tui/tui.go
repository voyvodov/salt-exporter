package tui

import (
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	teaList "github.com/charmbracelet/bubbles/list"
	teaViewport "github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kpetremann/salt-exporter/pkg/events"
)

const theme = "solarized-dark"

type format int

type listKeyMap struct {
	enableFollow   key.Binding
	toggleJSONYAML key.Binding
	toggleWordwrap key.Binding
}

func newListKeyMap() *listKeyMap {
	return &listKeyMap{
		enableFollow: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "follow mode"),
		),
		toggleWordwrap: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "toggle JSON word wrap"),
		),
		toggleJSONYAML: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "toggle JSON/YAML"),
		),
	}
}

type model struct {
	eventList      teaList.Model
	itemsBuffer    []teaList.Item
	rawView        teaViewport.Model
	eventChan      <-chan events.SaltEvent
	keys           *listKeyMap
	sideInfos      string
	terminalWidth  int
	terminalHeight int
	maxItems       int
	outputFormat   format
	followMode     bool
	wordWrap       bool
}

func NewModel(eventChan <-chan events.SaltEvent, maxItems int) model {
	var listKeys = newListKeyMap()

	list := teaList.NewDefaultDelegate()

	selColor := lipgloss.Color("#fcc203")
	list.Styles.SelectedTitle = list.Styles.SelectedTitle.Foreground(selColor).BorderLeftForeground(selColor)
	list.Styles.SelectedDesc = list.Styles.SelectedTitle.Copy()

	eventList := teaList.New([]teaList.Item{}, list, 0, 0)
	eventList.Title = "Events"
	eventList.Styles.Title = listTitleStyle
	eventList.AdditionalFullHelpKeys = func() []key.Binding {
		return []key.Binding{
			listKeys.enableFollow,
			listKeys.toggleWordwrap,
			listKeys.toggleJSONYAML,
		}
	}
	eventList.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			listKeys.enableFollow,
			listKeys.toggleJSONYAML,
		}
	}
	eventList.Filter = WordsFilter

	rawView := teaViewport.New(1, 1)
	rawView.KeyMap = teaViewport.KeyMap{}

	return model{
		eventList:  eventList,
		rawView:    rawView,
		keys:       listKeys,
		eventChan:  eventChan,
		followMode: true,
		maxItems:   maxItems,
	}
}

func watchEvent(m model) tea.Cmd {
	return func() tea.Msg {
		e := <-m.eventChan
		var sender string = "master"
		if e.Data.Id != "" {
			sender = e.Data.Id
		}
		eventJSON, err := e.RawToJSON(true)
		if err != nil {
			log.Fatalln(err)
		}
		eventYAML, err := e.RawToYAML()
		if err != nil {
			log.Fatalln(err)
		}
		datetime, _ := time.Parse("2006-01-02T15:04:05.999999", e.Data.Timestamp)
		item := item{
			title:       e.Tag,
			description: e.Type,
			datetime:    datetime.Format("2006-01-02 15:04"),
			event:       e,
			sender:      sender,
			state:       e.ExtractState(),
			eventJSON:   string(eventJSON),
			eventYAML:   string(eventYAML),
		}

		return item
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		watchEvent(m),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.eventList.Index() > 0 {
		m.followMode = false
	}

	if !m.followMode {
		m.eventList.Title = "Events (frozen)"
	} else {
		m.eventList.Title = "Events"
	}

	switch msg := msg.(type) {
	case item:
		m.itemsBuffer = append([]teaList.Item{msg}, m.itemsBuffer...)
		if len(m.itemsBuffer) > m.maxItems {
			m.itemsBuffer = m.itemsBuffer[:len(m.itemsBuffer)-1]
		}

		// When not in follow mode, we freeze the visible list.
		if m.followMode {
			cmds = append(cmds, m.eventList.SetItems(m.itemsBuffer))
		}
		cmds = append(cmds, watchEvent(m))

	case tea.WindowSizeMsg:
		m.terminalWidth = msg.Width
		m.terminalHeight = msg.Height

	case tea.KeyMsg:
		// Don't match any of the keys below if we're actively filtering.
		if m.eventList.FilterState() == teaList.Filtering {
			break
		}

		switch {
		case key.Matches(msg, m.keys.enableFollow):
			m.followMode = true
			m.eventList.ResetSelected()
			return m, nil
		case key.Matches(msg, m.keys.toggleWordwrap):
			m.wordWrap = !m.wordWrap
		case key.Matches(msg, m.keys.toggleJSONYAML):
			m.outputFormat = (m.outputFormat + 1) % nbFormat
		}
	}

	var cmd tea.Cmd
	m.eventList, cmd = m.eventList.Update(msg)
	cmds = append(cmds, cmd)

	if sel := m.eventList.SelectedItem(); sel != nil {
		switch m.outputFormat {
		case YAML:
			m.sideInfos = sel.(item).eventYAML
			if m.wordWrap {
				m.sideInfos = strings.ReplaceAll(m.sideInfos, "\\n", "  \\\n")
			}
			if info, err := Highlight(m.sideInfos, "yaml", theme); err != nil {
				m.rawView.SetContent(m.sideInfos)
			} else {
				m.rawView.SetContent(info)
			}
		case JSON:
			m.sideInfos = sel.(item).eventJSON
			if m.wordWrap {
				m.sideInfos = strings.ReplaceAll(m.sideInfos, "\\n", "  \\\n")
			}
			if info, err := Highlight(m.sideInfos, "json", theme); err != nil {
				m.rawView.SetContent(m.sideInfos)
			} else {
				m.rawView.SetContent(info)
			}
		}
	}

	m.rawView, cmd = m.rawView.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)

}

func (m model) View() string {
	// Top bar
	topBarStyle.Width(m.terminalWidth)
	topBar := topBarStyle.Render(appTitleStyle.Render("Salt live"))

	var content []string
	contentHeight := m.terminalHeight - lipgloss.Height(topBar)
	contentWidth := m.terminalWidth / 2

	// Left panel
	leftPanelStyle.Width(contentWidth)
	leftPanelStyle.Height(contentHeight)

	m.eventList.SetSize(
		contentWidth-leftPanelStyle.GetHorizontalFrameSize(),
		contentHeight-leftPanelStyle.GetVerticalFrameSize(),
	)

	content = append(content, leftPanelStyle.Render(m.eventList.View()))

	// Right panel

	if m.sideInfos != "" {
		rawTitle := rightPanelTitleStyle.Render("Raw details")

		rightPanelStyle.Width(contentWidth)
		rightPanelStyle.Height(contentHeight)

		m.rawView.Width = contentWidth - rightPanelStyle.GetHorizontalFrameSize()
		m.rawView.Height = contentHeight - lipgloss.Height(rawTitle) - rightPanelStyle.GetVerticalFrameSize()

		sideInfos := rightPanelStyle.Render(lipgloss.JoinVertical(0, rawTitle, m.rawView.View()))
		content = append(content, sideInfos)
	}

	// Final rendering
	return lipgloss.JoinVertical(0, topBar, lipgloss.JoinHorizontal(0, content...))
}
