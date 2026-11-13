import AppKit

// The launcher owns exactly one CLI process. All state changes happen on the main queue.
final class DropView: NSView {
    var openURL: ((URL) -> Void)?

    override init(frame: NSRect) {
        super.init(frame: frame)
        registerForDraggedTypes([.fileURL])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is unavailable") }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        sender.draggingPasteboard.canReadObject(forClasses: [NSURL.self], options: nil) ? .copy : []
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard let urls = sender.draggingPasteboard.readObjects(forClasses: [NSURL.self]) as? [URL],
              urls.count == 1, let url = urls.first else { return false }
        openURL?(url)
        return true
    }
}

final class Launcher: NSObject, NSApplicationDelegate {
    private var window: NSWindow!
    private var status = NSTextField(wrappingLabelWithString: "Choose a file or folder to open it in your default browser.")
    private var path = NSTextField(wrappingLabelWithString: "You can also drag a file or folder onto this window.")
    private var openFileButton: NSButton!
    private var openFolderButton: NSButton!
    private var browserButton: NSButton!
    private var stopButton: NSButton!
    private var process: Process?
    private var pipe: Pipe?
    private var output = ""
    private var serverURL: URL?
    private var stopping = false
    private var quitting = false
    private var pendingURL: URL?

    func applicationDidFinishLaunching(_ notification: Notification) {
        makeMenu()
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 570, height: 380),
                          styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "mdview"
        window.isReleasedWhenClosed = false
        let content = DropView(frame: window.contentView!.bounds)
        content.openURL = { [weak self] url in self?.open(url) }
        window.contentView = content

        let heading = NSTextField(labelWithString: "A small tool for reading documents comfortably")
        heading.font = .systemFont(ofSize: 24, weight: .semibold)
        status.font = .systemFont(ofSize: 13)
        status.textColor = .secondaryLabelColor
        path.font = .systemFont(ofSize: 12)
        path.textColor = .secondaryLabelColor
        path.maximumNumberOfLines = 3
        path.isSelectable = true
        openFileButton = NSButton(title: "Open File…", target: self, action: #selector(chooseFile))
        openFolderButton = NSButton(title: "Open Folder…", target: self, action: #selector(chooseFolder))
        openFolderButton.keyEquivalent = "\r"
        browserButton = NSButton(title: "Reopen Browser", target: self, action: #selector(reopenBrowser))
        stopButton = NSButton(title: "Stop Viewer", target: self, action: #selector(stop))
        let choices = NSStackView(views: [openFolderButton, openFileButton])
        choices.spacing = 12
        let controls = NSStackView(views: [browserButton, stopButton])
        controls.spacing = 12
        let note = NSTextField(wrappingLabelWithString: "Save any recording in the browser first.\nStopping the viewer or quitting the app also stops the document server.")
        note.font = .systemFont(ofSize: 11)
        note.textColor = .secondaryLabelColor
        let guide = NSButton(title: "Install & Usage Guide", target: self, action: #selector(showGuide))
        guide.bezelStyle = .inline
        let stack = NSStackView(views: [heading, status, choices, path, controls, note, guide])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 18
        stack.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 32),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -32),
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 30),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor, constant: -20)
        ])
        updateButtons()
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        if let url = pendingURL {
            pendingURL = nil
            open(url)
        }
    }

    private func makeMenu() {
        let menu = NSMenu()
        let appItem = NSMenuItem()
        menu.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "About mdview", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Quit mdview", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        let fileItem = NSMenuItem()
        menu.addItem(fileItem)
        let fileMenu = NSMenu(title: "File")
        fileMenu.addItem(withTitle: "Open File…", action: #selector(chooseFile), keyEquivalent: "o").target = self
        let folderItem = fileMenu.addItem(withTitle: "Open Folder…", action: #selector(chooseFolder), keyEquivalent: "o")
        folderItem.keyEquivalentModifierMask = [.command, .shift]
        folderItem.target = self
        fileMenu.addItem(withTitle: "Install & Usage Guide", action: #selector(showGuide), keyEquivalent: "?").target = self
        fileItem.submenu = fileMenu
        NSApp.mainMenu = menu
    }

    @objc private func chooseFile() { choose(directory: false) }
    @objc private func chooseFolder() { choose(directory: true) }

    private func choose(directory: Bool) {
        guard process == nil else { return }
        let panel = NSOpenPanel()
        panel.title = directory ? "Choose a Folder to Open" : "Choose a Document to Open"
        panel.prompt = "Open"
        panel.canChooseDirectories = directory
        panel.canChooseFiles = !directory
        panel.allowsMultipleSelection = false
        panel.beginSheetModal(for: window) { [weak self] result in
            if result == .OK, let url = panel.url { self?.open(url) }
        }
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        if let url = urls.first {
            if window == nil { pendingURL = url } else { open(url) }
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        window.makeKeyAndOrderFront(nil)
        return true
    }

    private func open(_ url: URL) {
        window.makeKeyAndOrderFront(nil)
        guard process == nil else {
            alert("The viewer is running", "Save any recording in the browser and click “Stop Viewer” before opening another document.")
            return
        }
        let target = url.standardizedFileURL
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: target.path, isDirectory: &directory) else {
            alert("File not found", target.path)
            return
        }
        let child = Process()
        child.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/mdview")
        child.currentDirectoryURL = directory.boolValue ? target : target.deletingLastPathComponent()
        child.arguments = directory.boolValue ? [] : [target.path]
        // No shell interpolation, config copying, administrator privileges or global install.
        let childPipe = Pipe()
        child.standardOutput = childPipe
        child.standardError = childPipe
        output = ""
        serverURL = nil
        stopping = false
        process = child
        pipe = childPipe
        path.stringValue = target.path
        status.stringValue = "Opening browser…"
        childPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { handle.readabilityHandler = nil; return }
            let chunk = String(decoding: data, as: UTF8.self)
            DispatchQueue.main.async { self?.receive(chunk) }
        }
        child.terminationHandler = { [weak self] task in
            DispatchQueue.main.async { self?.finished(task) }
        }
        do {
            try child.run()
            updateButtons()
        } catch {
            childPipe.fileHandleForReading.readabilityHandler = nil
            child.terminationHandler = nil
            process = nil
            pipe = nil
            status.stringValue = "Couldn’t start. Please choose the file or folder again."
            updateButtons()
            alert("Failed to start mdview", error.localizedDescription)
        }
    }

    private func receive(_ chunk: String) {
        output += chunk
        // Keep diagnostics bounded and parse only the CLI startup message.
        if output.count > 32_768 { output = String(output.suffix(16_384)) }
        if !stopping, serverURL == nil {
            for line in output.components(separatedBy: .newlines) where line.hasPrefix("Serving ") {
                if let range = line.range(of: " at http://localhost:", options: .backwards),
                   let url = URL(string: String(line[range.lowerBound...].dropFirst(4))) {
                    serverURL = url
                    status.stringValue = "Running · \(url.absoluteString)"
                    updateButtons()
                }
            }
        }
    }

    private func finished(_ child: Process) {
        guard process === child else { return }
        pipe?.fileHandleForReading.readabilityHandler = nil
        pipe = nil
        process = nil
        serverURL = nil
        let expected = stopping || quitting
        stopping = false
        status.stringValue = expected ? "Stopped. You can open another file or folder." : "The viewer has exited."
        updateButtons()
        if quitting {
            NSApp.reply(toApplicationShouldTerminate: true)
        } else if !expected && child.terminationStatus != 0 {
            alert("The viewer has exited", String(output.suffix(2000)))
        }
    }

    @objc private func reopenBrowser() {
        if let url = serverURL { NSWorkspace.shared.open(url) }
    }

    @objc private func stop() {
        guard let child = process, !stopping else { return }
        stopping = true
        status.stringValue = "Stopping… The viewer will stop once transcription and prompt post-processing finish."
        updateButtons()
        if child.isRunning { child.terminate() }
    }

    private func updateButtons() {
        openFileButton.isEnabled = process == nil
        openFolderButton.isEnabled = process == nil
        browserButton.isEnabled = serverURL != nil && !stopping
        stopButton.isEnabled = process != nil && !stopping
    }

    @objc private func showGuide() {
        if let url = Bundle.main.url(forResource: "INSTALL", withExtension: "md") {
            NSWorkspace.shared.open(url)
        }
    }

    private func alert(_ title: String, _ detail: String) {
        let dialog = NSAlert()
        dialog.messageText = title
        dialog.informativeText = detail
        dialog.runModal()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard process != nil else { return .terminateNow }
        quitting = true
        stop()
        return .terminateLater
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.regular)
let launcher = Launcher()
app.delegate = launcher
app.run()
