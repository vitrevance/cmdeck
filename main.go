package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"
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
	HasRun  bool
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

var (
	processStates = make(map[string]*ProcessState)
	config        Config
	configFile    string
	myApp         fyne.App
	myWindow      fyne.Window
	tabsContainer *container.AppTabs
	configWatcher *fsnotify.Watcher
)

//go:embed icon.png
var iconData []byte

func main() {
	configFile = "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	myApp = app.NewWithID("cmdeck")
	myWindow = myApp.NewWindow("cmdeck")
	myWindow.Resize(fyne.NewSize(700, 480))
	resourceIcon := fyne.NewStaticResource("icon.png", iconData)
	myApp.SetIcon(resourceIcon)

	if desk, ok := myApp.(desktop.App); ok {
		m := fyne.NewMenu("cmdeck",
			fyne.NewMenuItem("Show", func() {
				myWindow.Show()
			}),
			fyne.NewMenuItem("Hide", func() {
				myWindow.Hide()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Exit", func() {
				stopConfigWatcher()
				myApp.Quit()
			}),
		)
		desk.SetSystemTrayMenu(m)
		desk.SetSystemTrayIcon(resourceIcon)
	}

	myWindow.SetCloseIntercept(func() {
		myWindow.Hide()
	})

	loadConfigAndRefreshUI()

	go watchConfigFile()

	myWindow.ShowAndRun()
}

func loadConfigAndRefreshUI() {
	err := loadConfig(configFile)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		dialog.ShowError(err, myWindow)
		return
	}

	tabsData := convertConfigToTabsData(config)

	tabsContainer = container.NewAppTabs()

	for _, tabData := range tabsData {
		tabContent := createTabContent(tabData)
		tabsContainer.Append(container.NewTabItem(tabData.Title, tabContent))
	}

	myWindow.SetContent(tabsContainer)
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

func watchConfigFile() {
	var err error
	configWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("Error creating file watcher: %v\n", err)
		return
	}
	defer configWatcher.Close()

	err = configWatcher.Add(configFile)
	if err != nil {
		fmt.Printf("Error watching config file: %v\n", err)
		return
	}

	fmt.Printf("Watching config file: %s\n", configFile)

	for {
		select {
		case event, ok := <-configWatcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				fmt.Println("Config file modified, reloading...")
				time.Sleep(100 * time.Millisecond)
				loadConfigAndRefreshUI()
			}
		case err, ok := <-configWatcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("File watcher error: %v\n", err)
		}
	}
}

func stopConfigWatcher() {
	if configWatcher != nil {
		configWatcher.Close()
	}
}

func convertConfigToTabsData(config Config) []TabData {
	var tabsData []TabData

	for tabName, commands := range config.Tabs {
		var rows []RowData
		for cmdName, command := range commands {
			fullDesc := fmt.Sprintf("%s %v", command.Exec, command.Args)
			description := fullDesc
			if len(fullDesc) > 60 {
				description = fullDesc[:57] + "..."
			}

			rows = append(rows, RowData{
				Title:       cmdName,
				Description: description,
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
			actionButton.Text = ""
			statusLabel.SetText("Running")
			logsButton.Enable()
		} else {
			actionButton.SetIcon(theme.MediaPlayIcon())
			actionButton.Text = ""
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
				err := cmd.Start()
				if err != nil {
					state.Mutex.Lock()
					state.Output = append(state.Output, fmt.Sprintf("Failed to start process: %v", err))
					state.Running = false
					state.Mutex.Unlock()
					fyne.Do(updateButtonState)
					return
				}

				err = cmd.Wait()
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
	logText := strings.Join(output, "\n")

	logWidget := widget.NewMultiLineEntry()
	logWidget.SetText(logText)
	logWidget.Wrapping = fyne.TextWrapWord

	scrollContainer := container.NewVScroll(logWidget)
	scrollContainer.SetMinSize(fyne.NewSize(800, 500))

	customDialog := dialog.NewCustom(title, "Close", scrollContainer, myWindow)
	customDialog.Resize(fyne.NewSize(800, 500))
	customDialog.Show()
}
