package main

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	sortBySize = iota
	sortByName
	sortByItems
	sortModes
)

var sortNames = [sortModes]string{"size", "name", "items"}

const barWidth = 12

type UI struct {
	app     *tview.Application
	pages   *tview.Pages
	table   *tview.Table
	header  *tview.TextView
	footer  *tview.TextView
	scanTxt *tview.TextView

	rootPath string
	root     *Node
	cur      *Node
	rows     []*Node // rows[0] == nil means the ".." entry
	maxVal   int64   // largest child value, for bar scaling
	sortMode int
	apparent bool

	scanner  atomic.Pointer[Scanner]
	flashGen atomic.Int64
}

// val is the size metric currently displayed for a node.
func (ui *UI) val(n *Node) int64 {
	if ui.apparent {
		return n.Size
	}
	return n.Usage
}

// ---------------------------------------------------------------- table data

// tableContent feeds the table straight from ui.rows, so only visible cells
// are ever materialized regardless of directory size.
type tableContent struct {
	tview.TableContentReadOnly
	ui *UI
}

func (tc *tableContent) GetRowCount() int    { return len(tc.ui.rows) + 1 }
func (tc *tableContent) GetColumnCount() int { return 5 }

func (tc *tableContent) GetCell(row, col int) *tview.TableCell {
	ui := tc.ui
	if row == 0 {
		text := [5]string{"SIZE", "", "USED", "ITEMS", "NAME"}[col]
		c := tview.NewTableCell(" " + text + " ").
			SetTextColor(tcell.ColorYellow).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false)
		if col < 4 {
			c.SetAlign(tview.AlignRight)
		}
		return c
	}
	if row < 1 || row-1 >= len(ui.rows) {
		return tview.NewTableCell("")
	}
	n := ui.rows[row-1]
	if n == nil { // ".." row
		if col == 4 {
			return tview.NewTableCell(" /..").
				SetTextColor(tcell.ColorAqua).
				SetAttributes(tcell.AttrBold)
		}
		return tview.NewTableCell("")
	}
	switch col {
	case 0:
		return tview.NewTableCell(" " + humanBytes(ui.val(n)) + " ").
			SetAlign(tview.AlignRight).
			SetAttributes(tcell.AttrBold)
	case 1:
		color := tcell.ColorTeal
		if !n.IsDir {
			color = tcell.ColorGray
		}
		return tview.NewTableCell(bar(ui.val(n), ui.maxVal, barWidth)).
			SetTextColor(color)
	case 2:
		return tview.NewTableCell(" " + percent(ui.val(n), ui.val(ui.cur)) + " ").
			SetAlign(tview.AlignRight).
			SetTextColor(tcell.ColorGray)
	case 3:
		text := ""
		if n.IsDir {
			text = groupInt(n.Items)
		}
		return tview.NewTableCell(" " + text + " ").
			SetAlign(tview.AlignRight).
			SetTextColor(tcell.ColorGray)
	default:
		name := " " + n.Name
		c := tview.NewTableCell(name).SetExpansion(1)
		if n.IsDir {
			c.SetText(name + "/").
				SetTextColor(tcell.ColorAqua).
				SetAttributes(tcell.AttrBold)
		}
		if n.Denied {
			c.SetText(name + "  [access denied]").SetTextColor(tcell.ColorRed)
		}
		return c
	}
}

// ------------------------------------------------------------------- set up

func NewUI(rootPath string) *UI {
	ui := &UI{
		app:      tview.NewApplication(),
		pages:    tview.NewPages(),
		rootPath: rootPath,
	}

	ui.scanTxt = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	ui.pages.AddPage("scan", ui.scanTxt, true, true)

	ui.header = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	ui.footer = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	ui.setFooterKeys()

	ui.table = tview.NewTable().
		SetContent(&tableContent{ui: ui}).
		SetSelectable(true, false).
		SetFixed(1, 0)
	ui.table.SetSelectedFunc(func(row, col int) { ui.open(ui.nodeAt(row)) })
	ui.table.SetInputCapture(ui.handleKey)
	ui.table.SetBackgroundColor(tcell.ColorDefault)

	browse := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(ui.header, 2, 0, false).
		AddItem(ui.table, 0, 1, true).
		AddItem(ui.footer, 1, 0, false)
	ui.pages.AddPage("browse", browse, true, false)

	ui.app.SetRoot(ui.pages, true).EnableMouse(true)
	return ui
}

// Run scans the root path and starts the interactive loop.
func (ui *UI) Run() error {
	ui.scan(ui.rootPath, func(n *Node, err error) {
		if err != nil {
			ui.app.Stop()
			fmt.Println("macdisco:", err)
			return
		}
		ui.root = n
		ui.enter(n, "")
		ui.pages.SwitchToPage("browse")
	})
	return ui.app.Run()
}

// scan runs the scanner in the background with a live progress page and
// calls done on the UI goroutine when finished.
func (ui *UI) scan(path string, done func(*Node, error)) {
	s := NewScanner(path)
	ui.scanner.Store(s)
	ui.pages.SwitchToPage("scan")

	finished := make(chan struct{})
	go func() { // progress repaints
		tick := time.NewTicker(80 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-finished:
				return
			case <-tick.C:
				files, bytes := s.FilesSeen.Load(), s.BytesSeen.Load()
				ui.app.QueueUpdateDraw(func() {
					ui.scanTxt.SetText(fmt.Sprintf(
						"\n\n\n[::b]macdisco[-:-:-] %s\n\nscanning [aqua]%s[-]\n\n[yellow]%s[-] items · [yellow]%s[-]",
						version, tview.Escape(path), groupInt(files), humanBytes(bytes)))
				})
			}
		}
	}()
	go func() {
		start := time.Now()
		n, err := s.Scan(path)
		elapsed := time.Since(start)
		close(finished)
		ui.app.QueueUpdateDraw(func() {
			done(n, err)
			if err == nil {
				ui.flash(fmt.Sprintf("scanned %s items in %.1fs",
					groupInt(s.FilesSeen.Load()), elapsed.Seconds()))
			}
		})
	}()
}

// ---------------------------------------------------------------- navigation

// enter makes dir the current directory, optionally re-selecting a child.
func (ui *UI) enter(dir *Node, selectName string) {
	ui.cur = dir
	ui.rebuildRows()
	row := 1
	if selectName != "" {
		for i, n := range ui.rows {
			if n != nil && n.Name == selectName {
				row = i + 1
				break
			}
		}
	}
	if len(ui.rows) == 0 {
		row = 0
	}
	ui.table.Select(row, 0).ScrollToBeginning()
	ui.updateHeader()
}

// rebuildRows re-sorts the current directory into the display buffer.
func (ui *UI) rebuildRows() {
	children := append([]*Node(nil), ui.cur.Children...)
	sort.SliceStable(children, func(i, j int) bool {
		a, b := children[i], children[j]
		switch ui.sortMode {
		case sortByName:
			if a.IsDir != b.IsDir {
				return a.IsDir
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortByItems:
			return a.Items > b.Items
		default:
			return ui.val(a) > ui.val(b)
		}
	})
	ui.maxVal = 0
	for _, c := range children {
		if v := ui.val(c); v > ui.maxVal {
			ui.maxVal = v
		}
	}
	ui.rows = ui.rows[:0]
	if ui.cur.Parent != nil {
		ui.rows = append(ui.rows, nil)
	}
	ui.rows = append(ui.rows, children...)
}

func (ui *UI) nodeAt(row int) *Node {
	if row < 1 || row-1 >= len(ui.rows) {
		return nil
	}
	return ui.rows[row-1]
}

func (ui *UI) selected() *Node {
	row, _ := ui.table.GetSelection()
	return ui.nodeAt(row)
}

func (ui *UI) open(n *Node) {
	if n == nil { // ".."
		ui.up()
		return
	}
	if n.IsDir {
		ui.enter(n, "")
		return
	}
	ui.reportFinder(revealInFinder(n.Path()))
}

// reportFinder surfaces a failed open/reveal without interrupting the flow.
func (ui *UI) reportFinder(err error) {
	if err != nil {
		ui.flash("could not open Finder: " + err.Error())
	}
}

func (ui *UI) up() {
	if ui.cur.Parent != nil {
		from := ui.cur.Name
		ui.enter(ui.cur.Parent, from)
	}
}

// ------------------------------------------------------------------- header

func (ui *UI) updateHeader() {
	mode := "disk usage"
	if ui.apparent {
		mode = "apparent size"
	}
	free := ""
	var st syscall.Statfs_t
	if err := syscall.Statfs(ui.rootPath, &st); err == nil {
		free = " · free " + humanBytes(int64(st.Bavail)*int64(st.Bsize))
	}
	// left-truncate the path so its deepest (most useful) part stays visible
	path := []rune(ui.cur.Path())
	if _, _, w, _ := ui.header.GetInnerRect(); w > 14 && len(path) > w-11 {
		path = append([]rune("…"), path[len(path)-(w-12):]...)
	}
	ui.header.SetText(fmt.Sprintf(
		" [::b]macdisco[-:-:-] [aqua::b]%s[-:-:-]\n [gray]%s · %s items%s · %s · sort: %s[-]",
		tview.Escape(string(path)),
		humanBytes(ui.val(ui.cur)), groupInt(ui.cur.Items), free, mode, sortNames[ui.sortMode]))
}

func (ui *UI) setFooterKeys() {
	ui.footer.SetText(" [::b]⏎[-:-:-] open  [::b]⌫[-:-:-] up  [::b]t[-:-:-] trash  [::b]d[-:-:-] delete  [::b]f[-:-:-] reveal  [::b]o[-:-:-] finder  [::b]r[-:-:-] rescan  [::b]s[-:-:-] sort  [::b]a[-:-:-] size mode  [::b]?[-:-:-] help  [::b]q[-:-:-] quit")
}

// flash shows a transient status message in the footer.
func (ui *UI) flash(msg string) {
	gen := ui.flashGen.Add(1)
	ui.footer.SetText(" [yellow]" + tview.Escape(msg) + "[-]")
	go func() {
		time.Sleep(3 * time.Second)
		ui.app.QueueUpdateDraw(func() {
			if ui.flashGen.Load() == gen {
				ui.setFooterKeys()
			}
		})
	}()
}

// --------------------------------------------------------------------- keys

func (ui *UI) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEnter, tcell.KeyRight:
		ui.open(ui.selected())
		return nil
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		ui.up()
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q':
			ui.app.Stop()
			return nil
		case 'l':
			ui.open(ui.selected())
			return nil
		case 'h':
			ui.up()
			return nil
		case 's':
			ui.sortMode = (ui.sortMode + 1) % sortModes
			ui.keepSelection(func() { ui.rebuildRows() })
			ui.updateHeader()
			return nil
		case 'a':
			ui.apparent = !ui.apparent
			ui.keepSelection(func() { ui.rebuildRows() })
			ui.updateHeader()
			return nil
		case 'r':
			ui.rescan()
			return nil
		case 'f':
			if n := ui.selected(); n != nil {
				ui.reportFinder(revealInFinder(n.Path()))
			}
			return nil
		case 'o':
			ui.reportFinder(openInFinder(ui.cur.Path()))
			return nil
		case 't':
			ui.removeSelected(false)
			return nil
		case 'd':
			ui.removeSelected(true)
			return nil
		case '?':
			ui.showHelp()
			return nil
		}
	}
	return ev // let the table handle j/k/g/G, arrows, paging
}

// keepSelection re-runs fn while keeping the same node selected if possible.
func (ui *UI) keepSelection(fn func()) {
	sel := ui.selected()
	fn()
	row := 1
	if sel != nil {
		for i, n := range ui.rows {
			if n == sel {
				row = i + 1
				break
			}
		}
	}
	ui.table.Select(row, 0)
}

// ------------------------------------------------------------------ actions

func (ui *UI) removeSelected(permanent bool) {
	n := ui.selected()
	if n == nil {
		return
	}
	verb, doIt := "Move to Trash", moveToTrash
	warn := ""
	if permanent {
		verb, doIt = "Delete permanently", deletePermanently
		warn = "\n[red::b]This cannot be undone.[-:-:-]"
	}
	detail := humanBytes(ui.val(n))
	if n.IsDir {
		detail += fmt.Sprintf(" · %s items", groupInt(n.Items))
	}
	ui.confirm(
		fmt.Sprintf("%s?\n\n[aqua]%s[-]\n%s%s", verb, tview.Escape(n.Path()), detail, warn),
		verb,
		func() {
			ui.busy(verb + "…")
			go func() {
				err := doIt(n.Path())
				ui.app.QueueUpdateDraw(func() {
					ui.pages.RemovePage("busy")
					ui.app.SetFocus(ui.table)
					if err != nil {
						ui.showError(err.Error())
						return
					}
					row, _ := ui.table.GetSelection()
					detach(n)
					ui.rebuildRows()
					if row-1 >= len(ui.rows) {
						row = len(ui.rows)
					}
					ui.table.Select(row, 0)
					ui.updateHeader()
					ui.flash(verb + ": " + n.Name + " ✓")
				})
			}()
		})
}

func (ui *UI) rescan() {
	target := ui.cur
	ui.scan(target.Path(), func(fresh *Node, err error) {
		ui.pages.SwitchToPage("browse")
		if err != nil {
			ui.showError(err.Error())
			return
		}
		dSize := fresh.Size - target.Size
		dUsage := fresh.Usage - target.Usage
		dItems := fresh.Items - target.Items
		for a := target.Parent; a != nil; a = a.Parent {
			a.Size += dSize
			a.Usage += dUsage
			a.Items += dItems
		}
		target.Size, target.Usage, target.Items = fresh.Size, fresh.Usage, fresh.Items
		target.Denied = fresh.Denied
		target.Children = fresh.Children
		for _, c := range target.Children {
			c.Parent = target
		}
		ui.enter(target, "")
	})
}

// ------------------------------------------------------------------- modals

func (ui *UI) confirm(text, verb string, action func()) {
	modal := tview.NewModal().
		SetText(text).
		AddButtons([]string{"Cancel", verb}).
		SetDoneFunc(func(_ int, label string) {
			ui.pages.RemovePage("modal")
			ui.app.SetFocus(ui.table)
			if label == verb {
				action()
			}
		})
	ui.pages.AddPage("modal", modal, false, true)
}

func (ui *UI) busy(text string) {
	modal := tview.NewModal().SetText(text)
	ui.pages.AddPage("busy", modal, false, true)
}

func (ui *UI) showError(msg string) {
	modal := tview.NewModal().
		SetText("[red::b]Error[-:-:-]\n\n" + tview.Escape(msg)).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(int, string) {
			ui.pages.RemovePage("modal")
			ui.app.SetFocus(ui.table)
		})
	ui.pages.AddPage("modal", modal, false, true)
}

func (ui *UI) showHelp() {
	help := `[::b]macdisco[-:-:-] ` + version + `

[yellow]↑/↓ j/k[-]     move      [yellow]⏎ l →[-]    open dir / reveal file
[yellow]⌫ h ←[-]     go up     [yellow]g/G[-]      top / bottom
[yellow]t[-]  move to Trash (undoable)
[yellow]d[-]  delete permanently
[yellow]f[-]  reveal selection in Finder
[yellow]o[-]  open current folder in Finder
[yellow]r[-]  rescan current folder
[yellow]s[-]  sort: size / name / items
[yellow]a[-]  toggle disk usage / apparent size
[yellow]q[-]  quit`
	modal := tview.NewModal().
		SetText(help).
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(int, string) {
			ui.pages.RemovePage("modal")
			ui.app.SetFocus(ui.table)
		})
	ui.pages.AddPage("modal", modal, false, true)
}
