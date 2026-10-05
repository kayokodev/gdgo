# goldbot 🥇

A long-only gold (PAXGUSDT) trading bot in pure Go. Backtests, paper trades, and alerts via Telegram.

**Strategy**: EMA trend filter (20/50/200) + (20-bar Donchian breakout OR RSI pullback) entry, ATR stop/TP, 1-2% risk per trade.

**Modes**:
- `backtest`: replay history, no fees/slippage modeled
- `paper`: live candles, fake money, Telegram alerts, survives restarts

## Quick Start

### Backtest
```bash
go run . -mode backtest -interval 4h -candles 10000 -quiet
```

### Paper Mode (24/7)
```bash
export TG_TOKEN="your-bot-token"
export TG_CHAT="-your-chat-id"
go run . -mode paper -interval 1d -risk 0.02 -poll 5m -heartbeat
```

### Deploy to Railway (Free Trial)
See [RAILWAY_SETUP.md](RAILWAY_SETUP.md)

## Flags

```
-mode string         backtest | paper (default "backtest")
-interval string     5m 15m 1h 4h 1d (default "15m")
-candles int         backtest history length (default 1000)
-cash float          starting fake USDT (default 1000)
-risk float          equity % risked per trade (default 0.01)
-sl float            stop distance in ATRs (default 1.5)
-tp float            TP distance in ATRs (default 3)
-trail float         trailing stop ATRs (0 = off, default 0)
-adx float           min ADX to enter, chop filter (0 = off, default 0)
-minedge float       min TP distance as % of price (default 0.006)
-fee float           fee per side, 0.1% = 0.001 (default 0.001)
-state string        paper: state file path (default "state.json")
-reset               paper: delete saved state and start fresh
-heartbeat           paper: Telegram status on every closed candle
-poll duration       paper: check interval (default 5m)
-symbol string       Binance symbol (default "PAXGUSDT")
-quiet               backtest: print stats only, no trades
```

## Files

- `main.go` — bot code (all-in-one, stdlib only)
- `go.mod`, `go.sum` — dependencies (none; stdlib only)
- `state.json` — paper mode state (auto-created)
- `trades.csv` — paper mode trade log
- `Procfile` — Railway deployment config
- `RAILWAY_SETUP.md` — Railway deploy guide

## Results (Backtest 4.5 Years, 4h Candles)

| | return | max DD | trades | win rate | PF |
|---|---|---|---|---|---|
| bot (1d, 1% risk) | +20.4% | 3.83% | 40 | 52% | 1.97 |
| bot (1d, 2% risk) | +38.3% | 6.49% | 40 | 52% | 1.88 |
| baseline (hold EMA200) | +88.7% | 33% | 64 | — | — |
| buy&hold | +133% | — | 0 | — | — |

Gold had a historic bull run. The bot's value is smooth, low-drawdown rides, not beating holding.

## Disclaimer

This is a **paper-trading bot only**. Backtests don't include slippage, rejections, or real market friction. Live results will be worse. The bot is for learning, not production money. Trade at your own risk 🙏

## Telegram Alerts

With `-heartbeat` on, you get:
- 🥇 Start message
- 🟢 Entry alerts with size, SL, TP
- 🔴 Exit alerts with P&L
- 📊 Candle close updates (daily if on 1d candles)
- ⚠️ Warning after 10 failed fetches

## License

MIT. Use it, fork it, improve it 🚀

