# Deploy goldbot to Railway 🚂

## 1. Telegram Bot Setup (5 min)

If you haven't done this yet:

1. Message **@BotFather** on Telegram, send `/newbot`, copy the token
2. Send any message to your new bot, then open this in your browser:
   ```
   https://api.telegram.org/bot<YOUR_TOKEN>/getUpdates
   ```
   Find `"chat":{"id":...}` — that's your chat ID (keep the minus sign if it's negative)

## 2. Railway Setup (2 min)

1. Go to **railway.app** and sign up (no card required for $5 trial)
2. Create a new project, choose "Deploy from GitHub"
3. Connect your GitHub account and select this repo (or create one with goldbot files)

## 3. Add Environment Variables

In Railway's dashboard:

1. Go to **Variables**
2. Add:
   - `TG_TOKEN`: your bot token (from BotFather)
   - `TG_CHAT`: your chat ID (from getUpdates)
   - `PORT`: `3000` (Railway requires this, but the bot ignores it)

## 4. Deploy

Railway auto-detects Go projects. It will:
1. Read `go.mod` and `go.sum`
2. Build: `go build -o goldbot`
3. Run: reads `Procfile` → `./goldbot -mode paper ...`

Push to GitHub (or Railway can auto-build from your branch), and it starts.

## 5. Monitor

In Railway's dashboard:
- **Logs**: watch for entry/exit messages and Telegram alerts
- **Deployments**: restart or redeploy from here
- **Usage**: check if you're under the $5 trial credit or $1/month free tier

## 6. Stop or Delete

To stop trading without redeploying:
1. Go to **Variables** and add `RAILWAY_PAUSE=1` (the bot doesn't check this, so just delete the deployment instead)
2. Click **Delete** on the deployment → it shuts down
3. Download `state.json` from Railway's storage before deleting (see below)

## 7. Keep Your State (Important!)

The Procfile uses `/tmp/state.json`, which resets on redeploy. To save your trades:

1. Connect via SSH (Railway shows this in Deployments):
   ```
   ssh user@your-railway-hostname
   cat state.json > ~/state.json
   exit
   scp user@your-railway-hostname:~/state.json .
   ```
2. Or just check **Logs** for the exit messages and copy them to a file

## 8. Troubleshooting

**"Go not detected"** → Make sure `go.mod` and `go.sum` are committed to git.

**"Procfile not found"** → Railway didn't sync it. Push again or manually add the build command in Railway settings.

**"No Telegram alerts"** → Check that `TG_TOKEN` and `TG_CHAT` are in Variables. Test with:
   ```
   curl https://api.telegram.org/bot$TG_TOKEN/sendMessage -d chat_id="$TG_CHAT" -d text="test"
   ```

**"Fetch failed"** → Binance blocked or timeout. Railway's IP may be throttled. Wait 30 seconds and it usually recovers.

## 9. File Checklist

Before pushing to GitHub, make sure you have:
- ✅ `main.go` — the bot code
- ✅ `go.mod` and `go.sum` — dependencies
- ✅ `Procfile` — Railway run command
- ❌ `.env` or any file with secrets (Railway uses Variables instead)

## 10. Costs After Trial

- **First 30 days**: $5 free credit (no card required)
- **After 30 days**: Free tier gives $1/month (may require adding a card for recurring credit; unclear as of 2026-10 based on mixed docs)
- **If you add a paid plan**: Hobby is $5/month with $5 credit included. This bot barely uses anything, so $5/month covers it

Not financial advice 🙏 Paper trade for a few weeks before deciding anything is real.

