// goldbot: long-only gold bot on Binance PAXGUSDT (public candles, no API key).
// Modes: backtest (history replay) and paper (live candles, fake money).
// Strategy (mixed): EMA trend filter + (Donchian breakout OR RSI pullback) entry,
// ATR stop / take-profit, risk-based sizing, exit on stop / TP / trend flip.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Candle struct {
	Time          time.Time
	O, H, L, C, V float64
}

type Params struct {
	TrailATR, ADXMin  float64 // trailing-stop ATR mult (0 = off, use fixed TP); min ADX to enter (0 = off)
	MinEdge           float64 // fee guard: skip entry if TP distance < this fraction of price
	Fast, Slow, Trend int
	RSIN, Breakout    int
	ATRN              int
	StopATR, TPATR    float64
	RiskPct, Fee      float64
}

func defaultParams() Params {
	return Params{Fast: 20, Slow: 50, Trend: 200, RSIN: 14, Breakout: 20, ATRN: 14,
		StopATR: 1.5, TPATR: 3.0, RiskPct: 0.01, Fee: 0.001, MinEdge: 0.006, TrailATR: 0, ADXMin: 0}
}

// ---------- data ----------

// Mirrors tried in order (some ISPs block/throttle api.binance.com).
// The host that works gets moved to the front.
var quiet bool

var hosts = []string{"data-api.binance.vision", "api.binance.com", "api1.binance.com",
	"api2.binance.com", "api3.binance.com", "api4.binance.com"}

func fetchCandles(symbol, interval string, limit int, endMs int64) ([]Candle, error) {
	var lastErr error
	for i, h := range hosts {
		cs, err := fetchFrom(h, symbol, interval, limit, endMs)
		if err == nil {
			hosts[0], hosts[i] = hosts[i], hosts[0]
			return cs, nil
		}
		lastErr = fmt.Errorf("%s: %w", h, err)
	}
	return nil, lastErr
}

func fetchFrom(host, symbol, interval string, limit int, endMs int64) ([]Candle, error) {
	url := fmt.Sprintf("https://%s/api/v3/klines?symbol=%s&interval=%s&limit=%d", host, symbol, interval, limit)
	if endMs > 0 {
		url += fmt.Sprintf("&endTime=%d", endMs)
	}
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	var raw [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(raw))
	for _, k := range raw {
		if len(k) < 6 {
			continue
		}
		f := func(i int) float64 {
			s, _ := k[i].(string)
			v, _ := strconv.ParseFloat(s, 64)
			return v
		}
		ms, _ := k[0].(float64)
		out = append(out, Candle{time.UnixMilli(int64(ms)), f(1), f(2), f(3), f(4), f(5)})
	}
	return out, nil
}

// fetchHistory pages backwards (1000 candles per request) until `total` candles.
func fetchHistory(symbol, interval string, total int) ([]Candle, error) {
	var all []Candle
	var endMs int64
	for len(all) < total {
		batch, err := fetchCandles(symbol, interval, 1000, endMs)
		if err != nil {
			if len(all) == 0 {
				return nil, err
			}
			fmt.Println("history fetch stopped early:", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		all = append(batch, all...)
		endMs = batch[0].Time.UnixMilli() - 1
		fmt.Printf("fetched %d candles...\n", len(all))
		if len(batch) < 1000 {
			break
		}
	}
	if len(all) > total {
		all = all[len(all)-total:]
	}
	return all, nil
}

// ---------- indicators ----------

func ema(v []float64, n int) []float64 {
	out := make([]float64, len(v))
	k := 2.0 / float64(n+1)
	for i := range v {
		if i == 0 {
			out[i] = v[i]
		} else {
			out[i] = v[i]*k + out[i-1]*(1-k)
		}
	}
	return out
}

func rsi(c []float64, n int) []float64 {
	out := make([]float64, len(c))
	for i := range out {
		out[i] = 50
	}
	var ag, al float64
	for i := 1; i < len(c); i++ {
		d := c[i] - c[i-1]
		g, l := math.Max(d, 0), math.Max(-d, 0)
		if i <= n {
			ag += g / float64(n)
			al += l / float64(n)
			if i < n {
				continue
			}
		} else {
			ag = (ag*float64(n-1) + g) / float64(n)
			al = (al*float64(n-1) + l) / float64(n)
		}
		if al == 0 {
			out[i] = 100
		} else {
			out[i] = 100 - 100/(1+ag/al)
		}
	}
	return out
}

func atr(cs []Candle, n int) []float64 {
	out := make([]float64, len(cs))
	var a float64
	for i := 1; i < len(cs); i++ {
		tr := math.Max(cs[i].H-cs[i].L, math.Max(math.Abs(cs[i].H-cs[i-1].C), math.Abs(cs[i].L-cs[i-1].C)))
		if i <= n {
			a += tr / float64(n)
			if i < n {
				continue
			}
		} else {
			a = (a*float64(n-1) + tr) / float64(n)
		}
		out[i] = a
	}
	return out
}

// adx: Wilder ADX, used as a chop filter (low ADX = sideways market).
func adx(cs []Candle, n int) []float64 {
	out := make([]float64, len(cs))
	var trS, pS, mS, dxSum, a float64
	fn := float64(n)
	for i := 1; i < len(cs); i++ {
		up := cs[i].H - cs[i-1].H
		dn := cs[i-1].L - cs[i].L
		pdm, mdm := 0.0, 0.0
		if up > dn && up > 0 {
			pdm = up
		}
		if dn > up && dn > 0 {
			mdm = dn
		}
		tr := math.Max(cs[i].H-cs[i].L, math.Max(math.Abs(cs[i].H-cs[i-1].C), math.Abs(cs[i].L-cs[i-1].C)))
		if i <= n {
			trS += tr
			pS += pdm
			mS += mdm
		} else {
			trS = trS - trS/fn + tr
			pS = pS - pS/fn + pdm
			mS = mS - mS/fn + mdm
		}
		if i < n || trS == 0 {
			continue
		}
		pdi, mdi := 100*pS/trS, 100*mS/trS
		dx := 0.0
		if pdi+mdi > 0 {
			dx = 100 * math.Abs(pdi-mdi) / (pdi + mdi)
		}
		switch {
		case i < 2*n-1:
			dxSum += dx
		case i == 2*n-1:
			dxSum += dx
			a = dxSum / fn
			out[i] = a
		default:
			a = (a*(fn-1) + dx) / fn
			out[i] = a
		}
	}
	return out
}

type Ind struct{ F, S, T, R, A, D []float64 }

func calc(cs []Candle, p Params) Ind {
	c := make([]float64, len(cs))
	for i := range cs {
		c[i] = cs[i].C
	}
	return Ind{ema(c, p.Fast), ema(c, p.Slow), ema(c, p.Trend), rsi(c, p.RSIN), atr(cs, p.ATRN), adx(cs, 14)}
}

// ---------- strategy ----------

func entrySignal(cs []Candle, ind Ind, i int, p Params) (bool, string) {
	if i < p.Trend || i < p.Breakout+1 {
		return false, ""
	}
	if !(ind.F[i] > ind.S[i] && cs[i].C > ind.T[i]) { // trend filter
		return false, ""
	}
	if p.ADXMin > 0 && ind.D[i] < p.ADXMin { // chop filter
		return false, ""
	}
	hh := 0.0
	for j := i - p.Breakout; j < i; j++ {
		hh = math.Max(hh, cs[j].H)
	}
	if cs[i].C > hh && ind.R[i] < 75 {
		return true, "breakout"
	}
	if ind.R[i-1] < 45 && ind.R[i] >= 45 {
		return true, "rsi-pullback"
	}
	return false, ""
}

// ---------- paper engine ----------

type Position struct {
	HH float64 // highest high since entry (trailing stop)
	Qty, Entry, Stop, TP float64
	Open                 time.Time
}

type Trade struct {
	In, Out             time.Time
	Entry, Exit, Qty, PnL float64
	Why                 string
}

type Engine struct {
	Notify  func(string) // optional alert hook (Telegram)
	P       Params
	Cash    float64
	Pos     *Position
	Trades  []Trade
	Peak    float64
	MaxDD   float64
	OnTrade func(Trade)
	Logf    func(string, ...interface{})
}

func (e *Engine) equity(px float64) float64 {
	if e.Pos != nil {
		return e.Cash + e.Pos.Qty*px
	}
	return e.Cash
}

func (e *Engine) notify(f string, a ...interface{}) {
	if e.Notify != nil {
		e.Notify(fmt.Sprintf(f, a...))
	}
}

func (e *Engine) logf(f string, a ...interface{}) {
	if e.Logf != nil {
		e.Logf(f, a...)
	}
}

func (e *Engine) closePos(c Candle, px float64, why string) {
	pos := e.Pos
	proceeds := pos.Qty * px * (1 - e.P.Fee)
	pnl := proceeds - pos.Qty*pos.Entry*(1+e.P.Fee)
	e.Cash += proceeds
	e.Pos = nil
	t := Trade{pos.Open, c.Time, pos.Entry, px, pos.Qty, pnl, why}
	e.Trades = append(e.Trades, t)
	e.logf("EXIT  %s @ %.2f  pnl %+.2f USDT", why, px, pnl)
	msg := ""
	if pnl > 0 {
		msg = fmt.Sprintf("✅ *we're out with a W* — %s\n\nin @ %.2f out @ %.2f\n\n*+%.2f USDT* 💰\ncash now: %.2f\n\nnice execution bro 🔥", why, pos.Entry, px, pnl, e.Cash)
	} else {
		msg = fmt.Sprintf("🔴 *took the L* — %s\n\nin @ %.2f out @ %.2f\n\n%.2f USDT down\ncash: %.2f\n\nit's part of the game, we move 💪", why, pos.Entry, px, pnl, e.Cash)
	}
	e.notify(msg)
	if e.OnTrade != nil {
		e.OnTrade(t)
	}
}

// Step processes ONE closed candle at index i.
func (e *Engine) Step(cs []Candle, ind Ind, i int) {
	c := cs[i]
	exited := false
	if e.Pos != nil {
		switch {
		case c.L <= e.Pos.Stop: // stop checked first = conservative
			e.closePos(c, math.Min(c.O, e.Pos.Stop), "stop")
			exited = true
		case e.P.TrailATR == 0 && c.H >= e.Pos.TP: // fixed TP only when not trailing
			e.closePos(c, math.Max(c.O, e.Pos.TP), "take-profit")
			exited = true
		case ind.F[i] < ind.S[i]:
			e.closePos(c, c.C, "trend-flip")
			exited = true
		}
		if e.Pos != nil && e.P.TrailATR > 0 { // ratchet trailing stop up, never down
			e.Pos.HH = math.Max(e.Pos.HH, c.H)
			if ns := e.Pos.HH - e.P.TrailATR*ind.A[i]; ns > e.Pos.Stop {
				e.Pos.Stop = ns
			}
		}
	}
	if e.Pos == nil && !exited {
		if ok, why := entrySignal(cs, ind, i, e.P); ok {
			stopDist := e.P.StopATR * ind.A[i]
			if stopDist > 0 && e.P.TPATR*ind.A[i]/c.C >= e.P.MinEdge {
				qty := e.equity(c.C) * e.P.RiskPct / stopDist
				qty = math.Min(qty, e.Cash/(c.C*(1+e.P.Fee)))
				if qty*c.C >= 10 { // min notional
					e.Cash -= qty * c.C * (1 + e.P.Fee)
					e.Pos = &Position{HH: c.C, Qty: qty, Entry: c.C, Stop: c.C - stopDist, TP: c.C + e.P.TPATR*ind.A[i], Open: c.Time}
					e.logf("ENTRY %s @ %.2f  qty %.5f  SL %.2f  TP %.2f", why, c.C, qty, e.Pos.Stop, e.Pos.TP)
					e.notify(fmt.Sprintf("🟢 *ENTRY* — just went long on %s\n\ngold @ *%.2f* 🥇\nyou're in: %.5f PAXG\n\n*watch these levels:*\n📍 Stop: %.2f (don't go past this)\n🎯 Target: %.2f (take profit here)\n\nrisk: %.2f%% of your cash\n\nlet's ride this one bro 🚀", why, c.C, qty, e.Pos.Stop, e.Pos.TP, e.P.RiskPct*100))
				}
			}
		}
	}
	eq := e.equity(c.C)
	e.Peak = math.Max(e.Peak, eq)
	if e.Peak > 0 {
		e.MaxDD = math.Max(e.MaxDD, (e.Peak-eq)/e.Peak)
	}
}

// ---------- modes ----------

func report(e *Engine, cs []Candle, start int, startCash float64) {
	wins, gp, gl := 0, 0.0, 0.0
	for _, t := range e.Trades {
		if t.PnL > 0 {
			wins++
			gp += t.PnL
		} else {
			gl -= t.PnL
		}
	}
	n := len(e.Trades)
	last := cs[len(cs)-1].C
	pf := math.Inf(1)
	if gl > 0 {
		pf = gp / gl
	}
	wr := 0.0
	if n > 0 {
		wr = float64(wins) / float64(n) * 100
	}
	fmt.Printf("\n--- backtest ---\ncandles: %d | trades: %d | win rate: %.0f%%\n", len(cs)-start, n, wr)
	fmt.Printf("return: %+.2f%% | buy&hold: %+.2f%% | max drawdown: %.2f%% | profit factor: %.2f\n",
		(e.equity(last)/startCash-1)*100, (last/cs[start].C-1)*100, e.MaxDD*100, pf)
}

// baseline: hold while close > EMA200, otherwise cash. Fee on every switch.
// A dumb benchmark the bot has to beat.
func baseline(cs []Candle, ind Ind, start int, cash, fee float64) {
	eq, peak, maxDD := cash, cash, 0.0
	in, switches := false, 0
	for i := start; i < len(cs); i++ {
		if in {
			eq *= cs[i].C / cs[i-1].C // earn this candle's move while held
		}
		want := cs[i].C > ind.T[i] // decided at close, applies from next candle
		if want != in {
			eq *= 1 - fee
			in = want
			switches++
		}
		peak = math.Max(peak, eq)
		maxDD = math.Max(maxDD, (peak-eq)/peak)
	}
	fmt.Printf("\n--- baseline: hold above EMA%d, else cash ---\nswitches: %d | return: %+.2f%% | max drawdown: %.2f%%\n",
		start, switches, (eq/cash-1)*100, maxDD*100)
}

func backtest(p Params, symbol, interval string, cash float64, n int) {
	cs, err := fetchHistory(symbol, interval, n)
	if err != nil || len(cs) < p.Trend+10 {
		fmt.Println("fetch failed:", err)
		return
	}
	ind := calc(cs, p)
	e := &Engine{P: p, Cash: cash, Peak: cash}
	if !quiet {
		e.Logf = func(f string, a ...interface{}) { fmt.Printf(f+"\n", a...) }
	}
	for i := p.Trend; i < len(cs); i++ {
		e.Step(cs, ind, i)
	}
	report(e, cs, p.Trend, cash)
	baseline(cs, ind, p.Trend, cash, p.Fee)
}

// ---------- state + alerts ----------

type State struct {
	Cash  float64   `json:"cash"`
	Pos   *Position `json:"pos"`
	Peak  float64   `json:"peak"`
	MaxDD float64   `json:"max_dd"`
	Last  time.Time `json:"last"`
}

func saveState(path string, e *Engine, last time.Time) error {
	b, err := json.MarshalIndent(State{e.Cash, e.Pos, e.Peak, e.MaxDD, last}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic swap so a crash can't corrupt the file
}

func loadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// telegram sends a message if TG_TOKEN and TG_CHAT env vars are set (no-op otherwise).
func telegram(msg string) {
	token, chat := os.Getenv("TG_TOKEN"), os.Getenv("TG_CHAT")
	if token == "" || chat == "" {
		return
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).PostForm(
		"https://api.telegram.org/bot"+token+"/sendMessage",
		url.Values{"chat_id": {chat}, "text": {msg}})
	if err != nil {
		// scrub the token: Go error text includes the full URL
		fmt.Println("telegram send failed:", strings.ReplaceAll(err.Error(), token, "***"))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Println("telegram send failed: http", resp.StatusCode)
	}
}

func paper(p Params, symbol, interval string, cash float64, poll time.Duration, statePath string, reset, heartbeat bool) {
	e := &Engine{P: p, Cash: cash, Peak: cash}
	e.Logf = func(f string, a ...interface{}) {
		fmt.Printf("[%s] "+f+"\n", append([]interface{}{time.Now().Format("01-02 15:04:05")}, a...)...)
	}
	e.Notify = telegram
	f, err := os.OpenFile("trades.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("cant open trades.csv:", err)
		return
	}
	w := csv.NewWriter(f)
	e.OnTrade = func(t Trade) {
		w.Write([]string{t.In.Format(time.RFC3339), t.Out.Format(time.RFC3339),
			fmt.Sprintf("%.2f", t.Entry), fmt.Sprintf("%.2f", t.Exit),
			fmt.Sprintf("%.5f", t.Qty), fmt.Sprintf("%.2f", t.PnL), t.Why})
		w.Flush()
		emoji := "🔴"
		verdict := "took the L, moving on 💪"
		mood := "stop hit, happens 🙏"
		if t.PnL > 0 {
			emoji = "✅"
			verdict = "we're out with a W 🍾"
			mood = "nice one bro 🚀"
		}
		e.notify(fmt.Sprintf("%s *closing this trade* — %s\n\n%s\n\n*Entered:* %.2f\n*Exited:* %.2f\n*Size:* %.5f\n\n💰 *Result: %+.2f USDT*\n\n%s", emoji, t.Why, verdict, t.Entry, t.Exit, t.Qty, t.PnL, mood))
	}

	var last time.Time
	if reset {
		os.Remove(statePath)
	}
	if st, lerr := loadState(statePath); lerr == nil {
		e.Cash, e.Pos, e.Peak, e.MaxDD, last = st.Cash, st.Pos, st.Peak, st.MaxDD, st.Last
		e.logf("resumed from %s | cash %.2f | position open: %v | last candle %s",
			statePath, e.Cash, e.Pos != nil, last.Format("01-02 15:04"))
		posStr := "waiting in cash for the next signal 📍"
		if e.Pos != nil {
			posStr = fmt.Sprintf("got an open position @ %.2f 💪", e.Pos.Entry)
		}
		e.notify(fmt.Sprintf("🔄 *i'm back online*\n\nyour cash: %.2f USDT\nposition: %s\nlast candle: %s\n\npicked up where i left off, still watching 👀", e.Cash, posStr, last.Format("01-02 15:04")))
	} else if !os.IsNotExist(lerr) {
		e.logf("could not read %s (%v), starting fresh", statePath, lerr)
	}

	fails := 0
	for {
		cs, err := fetchCandles(symbol, interval, 1000, 0)
		if err != nil || len(cs) < p.Trend+10 {
			fails++
			e.logf("fetch error: %v", err)
			if fails == 10 {
				e.notify("⚠️ *yo, binance is being flakey*\n\n10 failed fetches in a row, but we're not giving up 💪\nstill retrying, should be back soon 🙏")
			}
			time.Sleep(poll)
			continue
		}
		fails = 0
		cs = cs[:len(cs)-1] // drop the still-forming candle
		if last.IsZero() {
			last = cs[len(cs)-1].Time
			if err := saveState(statePath, e, last); err != nil {
				e.logf("state save failed: %v", err)
			}
			e.logf("watching %s %s | last close %.2f | cash %.2f", symbol, interval, cs[len(cs)-1].C, e.Cash)
			e.notify(fmt.Sprintf("🥇 *yo, goldbot is running*\n\nscanning %s on %s candles\nyour cash: %.2f USDT\ngold rn: %.2f\n\ni'll watch for setups and ping you when i find one 👀 sit tight bro 🚀", symbol, interval, e.Cash, cs[len(cs)-1].C))
		} else {
			ind := calc(cs, p)
			for i := range cs { // catches up on any candles missed while the bot was down
				if !cs[i].Time.After(last) {
					continue
				}
				e.Step(cs, ind, i)
				last = cs[i].Time
				e.logf("candle close %.2f | equity %.2f | maxDD %.2f%%", cs[i].C, e.equity(cs[i].C), e.MaxDD*100)
				if heartbeat {
					posStr := "no position, waiting for a signal 📍"
					if e.Pos != nil {
						pct := (cs[i].C - e.Pos.Entry) / e.Pos.Entry * 100
						posStr = fmt.Sprintf("IN TRADE @ %.2f (%+.2f%%)", e.Pos.Entry, pct)
					}
					e.notify(fmt.Sprintf("📊 *daily update*\n\ngold price: *%.2f* 🥇\nyour equity: *%.2f USDT*\nmax heat taken: %.2f%%\n\nposition: %s\n\nstill on it 👀", cs[i].C, e.equity(cs[i].C), e.MaxDD*100, posStr))
				}
				if err := saveState(statePath, e, last); err != nil {
					e.logf("state save failed: %v", err)
				}
			}
		}
		time.Sleep(poll)
	}
}

func main() {
	p := defaultParams()
	mode := flag.String("mode", "backtest", "backtest | paper")
	symbol := flag.String("symbol", "PAXGUSDT", "Binance symbol (PAXG = gold-backed token)")
	interval := flag.String("interval", "15m", "candle interval: 5m 15m 1h 4h")
	cash := flag.Float64("cash", 1000, "starting fake USDT")
	poll := flag.Duration("poll", 20*time.Second, "paper-mode poll interval")
	candles := flag.Int("candles", 1000, "backtest history length in candles (pages 1000 at a time)")
	flag.Float64Var(&p.RiskPct, "risk", p.RiskPct, "equity fraction risked per trade (0.01 = 1%)")
	flag.Float64Var(&p.StopATR, "sl", p.StopATR, "stop distance in ATRs")
	flag.Float64Var(&p.TPATR, "tp", p.TPATR, "take-profit distance in ATRs")
	statePath := flag.String("state", "state.json", "paper mode: state file, lets the bot resume after a restart")
	reset := flag.Bool("reset", false, "paper mode: delete saved state and start fresh")
	heartbeat := flag.Bool("heartbeat", false, "paper mode: Telegram status on every closed candle")
	flag.Float64Var(&p.Fee, "fee", p.Fee, "fee per side (0.001 = 0.1%; try 0.00075 with BNB discount)")
	flag.Float64Var(&p.TrailATR, "trail", p.TrailATR, "trailing stop in ATRs (0 = off, uses fixed TP)")
	flag.Float64Var(&p.ADXMin, "adx", p.ADXMin, "min ADX to enter, chop filter (0 = off)")
	flag.BoolVar(&quiet, "quiet", false, "backtest: print stats only")
	flag.Float64Var(&p.MinEdge, "minedge", p.MinEdge, "min TP distance as fraction of price (0.006 = 0.6%)")
	flag.Parse()
	switch *mode {
	case "paper":
		paper(p, *symbol, *interval, *cash, *poll, *statePath, *reset, *heartbeat)
	default:
		backtest(p, *symbol, *interval, *cash, *candles)
	}
}

