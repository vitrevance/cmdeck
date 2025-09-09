package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Tabs map[string]map[string]Command `yaml:"tabs"`
}
type Command struct {
	Exec string   `yaml:"exec"`
	Args []string `yaml:"args"`
}
type ProcessState struct {
	Cmd     *exec.Cmd
	Running bool
	Output  []string
	Mutex   sync.Mutex
	HasRun  bool // Track if this process has been run at least once
}
type TabData struct {
	Title string
	Rows  []RowData
}
type RowData struct {
	Title       string
	Description string
	Command     Command
}

var processStates = make(map[string]*ProcessState)
var config Config

func main() {
	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	err := loadConfig(configFile)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}
	myApp := app.New()
	myWindow := myApp.NewWindow("Command Runner")
	myWindow.Resize(fyne.NewSize(600, 480))
	tabsData := convertConfigToTabsData(config)
	tabs := container.NewAppTabs()

	for _, tabData := range tabsData {
		tabContent := createTabContent(tabData)
		tabs.Append(container.NewTabItem(tabData.Title, tabContent))
	}

	myWindow.SetContent(tabs)
	myWindow.ShowAndRun()
}

func loadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	return nil
}

func convertConfigToTabsData(config Config) []TabData {
	var tabsData []TabData

	for tabName, commands := range config.Tabs {
		var rows []RowData
		for cmdName, command := range commands {
			rows = append(rows, RowData{
				Title:       cmdName,
				Description: fmt.Sprintf("%s %v", command.Exec, command.Args),
				Command:     command,
			})
		}
		tabsData = append(tabsData, TabData{
			Title: tabName,
			Rows:  rows,
		})
	}

	return tabsData
}

func createTabContent(tabData TabData) fyne.CanvasObject {
	listContainer := container.NewVBox()

	for _, row := range tabData.Rows {
		rowWidget := createRowWidget(row)
		listContainer.Add(rowWidget)
	}

	listContainer.Add(layout.NewSpacer())
	return container.NewVScroll(listContainer)
}

func createRowWidget(row RowData) *fyne.Container {
	processKey := fmt.Sprintf("%s-%s", row.Title, row.Description)
	if _, exists := processStates[processKey]; !exists {
		processStates[processKey] = &ProcessState{
			Output: []string{"No logs available. Run the process to see logs."},
		}
	}
	titleLabel := widget.NewLabel(row.Title)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	descLabel := widget.NewLabel(row.Description)
	descLabel.TextStyle = fyne.TextStyle{Italic: true}
	statusLabel := widget.NewLabel("Stopped")
	statusLabel.Alignment = fyne.TextAlignTrailing
	actionButton := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), nil)
	actionButton.Importance = widget.SuccessImportance
	logsButton := widget.NewButtonWithIcon("Logs", theme.DocumentIcon(), nil)
	logsButton.Importance = widget.MediumImportance
	updateButtonState := func() {
		state := processStates[processKey]
		if state.Running {
			actionButton.SetIcon(theme.MediaStopIcon())
			actionButton.Text = "" // Clear text to show only icon
			statusLabel.SetText("Running")
			logsButton.Enable()
		} else {
			actionButton.SetIcon(theme.MediaPlayIcon())
			actionButton.Text = "" // Clear text to show only icon
			statusLabel.SetText("Stopped")
			if state.HasRun {
				logsButton.Enable()
			} else {
				logsButton.Disable()
			}
		}
		actionButton.Refresh()
		logsButton.Refresh()
		statusLabel.Refresh()
	}
	actionButton.OnTapped = func() {
		state := processStates[processKey]
		if state.Running {
			state.Mutex.Lock()
			if state.Cmd != nil && state.Cmd.Process != nil {
				state.Cmd.Process.Kill()
			}
			state.Running = false
			state.Mutex.Unlock()
			fyne.Do(updateButtonState)
		} else {
			state.Mutex.Lock()
			state.Output = []string{fmt.Sprintf("Starting process: %s %v", row.Command.Exec, row.Command.Args)}
			state.HasRun = true
			state.Mutex.Unlock()
			cmd := exec.Command(row.Command.Exec, row.Command.Args...)
			stdoutPipe, err := cmd.StdoutPipe()
			if err != nil {
				fmt.Printf("Error creating stdout pipe: %v\n", err)
				state.Mutex.Lock()
				state.Output = append(state.Output, fmt.Sprintf("Error creating stdout pipe: %v", err))
				state.Mutex.Unlock()
				return
			}

			stderrPipe, err := cmd.StderrPipe()
			if err != nil {
				fmt.Printf("Error creating stderr pipe: %v\n", err)
				state.Mutex.Lock()
				state.Output = append(state.Output, fmt.Sprintf("Error creating stderr pipe: %v", err))
				state.Mutex.Unlock()
				return
			}
			state.Mutex.Lock()
			state.Cmd = cmd
			state.Running = true
			state.Mutex.Unlock()

			fyne.Do(updateButtonState)
			go captureOutput(stdoutPipe, processKey, false)
			go captureOutput(stderrPipe, processKey, true)
			go func() {
				err := cmd.Run()

				state.Mutex.Lock()
				state.Running = false
				if err != nil {
					state.Output = append(state.Output, fmt.Sprintf("Process exited with error: %v", err))
				} else {
					state.Output = append(state.Output, "Process completed successfully")
				}
				state.Mutex.Unlock()
				fyne.Do(updateButtonState)
			}()
		}
	}
	logsButton.OnTapped = func() {
		state := processStates[processKey]
		state.Mutex.Lock()
		output := make([]string, len(state.Output))
		copy(output, state.Output)
		state.Mutex.Unlock()
		showLogsDialog(row.Title, output)
	}
	fyne.Do(updateButtonState)
	textContent := container.NewVBox(
		titleLabel,
		descLabel,
	)
	mainRow := container.NewHBox(
		textContent,
		layout.NewSpacer(),
		statusLabel,
		container.NewPadded(logsButton),
		container.NewPadded(actionButton),
	)
	paddedRow := container.NewPadded(mainRow)
	borderedRow := container.NewBorder(
		nil,
		widget.NewSeparator(),
		nil,
		nil,
		paddedRow,
	)

	return borderedRow
}

func captureOutput(reader io.ReadCloser, processKey string, isStderr bool) {
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		state, exists := processStates[processKey]
		if !exists {
			return
		}

		state.Mutex.Lock()
		prefix := ""
		if isStderr {
			prefix = "ERR: "
		}
		state.Output = append(state.Output, prefix+line)
		state.Mutex.Unlock()
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading output: %v\n", err)
		state, exists := processStates[processKey]
		if exists {
			state.Mutex.Lock()
			state.Output = append(state.Output, fmt.Sprintf("Error reading output: %v", err))
			state.Mutex.Unlock()
		}
	}
}

func showLogsDialog(title string, output []string) {
	logText := ""
	for _, line := range output {
		logText += line + "\n"
	}
	logWidget := widget.NewMultiLineEntry()
	logWidget.SetText(logText)
	logWidget.Wrapping = fyne.TextWrapWord
	scrollContainer := container.NewVScroll(logWidget)
	scrollContainer.SetMinSize(fyne.NewSize(800, 500))
	customDialog := dialog.NewCustom(title, "Close", scrollContainer, fyne.CurrentApp().Driver().AllWindows()[0])
	customDialog.Resize(fyne.NewSize(800, 500))
	customDialog.Show()
}
