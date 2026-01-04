## Let's make it simple. If you want to use this project on your Mac<br />
### I - Technically you can run it through your terminal

1. Open your terminal
2. Go in the project folder
3. Run "go run ." <br />
4. Enjoy! :heart:

<img width="461" height="196" alt="image" src="https://github.com/user-attachments/assets/0283c2e6-9911-47ca-a57f-260406401961" />

### II - Run it as a service

You can run this app in the background of your computer.
To do so:

1. Open your terminal
2. Go to the project directory and build the project with `go build -o AM-RP`
3. Put the file that's been created (it should be `AM-RP`) in `~/go/bin`
4. Go to `/Users/YOUR_USER/Library/LaunchAgents/`
5. Create a file "AM-RP.plist" and put this in it:
```
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>AM-RP</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Users/YOUR_USER/go/bin/AM-RP</string> 
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/AM-RP.out.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/AM-RP.err.log</string>
</dict>
</plist>
```
:warning: Make sure to replace YOUR_USER by your actual macOS user...

4. Save the file
5. The app should ask you few permissions to access the Music app and the System Events and bim<br/>

<img width="457" height="198" alt="image" src="https://github.com/user-attachments/assets/dc5a113b-acc8-487e-b743-23bbcaeceaab" />
