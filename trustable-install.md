# ![](./web/trustable-logo.png){ height=50px } Trustable - Istruzioni per l'installazione

> **Nota:** La release è al momento in versione alpha ed è possibile che contenga errori. Usare il sito  `github.com/trustable-ai` per riportare problemi.

##  Sicurezza Informatica

Trustable è un sistema di vibe coding che genera applicazioni quindi richiede la possibilità di eseguire codice affidato alla AI.

Per ridurre i rischi, agisce solamente all'interno di container Docker e scrive solo su alcune cartelle nella home directory:

- `.local/bin`
- `.ops`
- `.ops-workspace`

Trustable richiede per l'installazione e l'esecuzione il download di eseguibili binari. Tutti i binari sono scaricati da fonti note e documentate.

> **IMPORTANTE** La AI agisce solo all'interno di un container Docker e non ha **mai** accesso diretto al filesystem locale dell'utente. I comandi sono eseguiti all'interno di un docker container.

I rischi di danni sono quindi limitati, ma è sempre bene essere cauti e non usare un computer di produzione o con dati sensibili.

## Prerequisiti

### Memoria

Trusable richiede un sistema con almeno **16 GB di memoria RAM**. È **molto improbabile** che funzioni accettabilmente con meno memoria.

### Sistemi operativi supportati

Trustable è stato testato su:

- Windows 11
- macOS Sequoia
- Ubuntu 24.04

Sistemi diversi o meno recenti possono presentare problemi di compatibilità.


### Windows Defender

Windows Defender interferisce con Trustable, rallentando l'esecuzione e impedendo il download di librarie.

Per l'esecuzione occorre disabilitare:

- Real TIme Detection (altrimenti rallenta notevolmente l'esecuzione)
- Network e Firewall protection (altrimenti blocca il download di eseguibili e librerie)

> **Importante**: Si raccomanda di installare Trustable in una macchina **CHE NON CONTIENE DATI SENSIBILI** e di cui si abbia un **completo backup**.

### Docker Desktop

Trustable richiede ed utilizza Docker. Occorre scaricare e installare **Docker Desktop** prima di cominciare e averlo in esecuzione.

> **Attenzione:** Docker Desktop richiede una login per scaricare le immagini. Effettuare il login prima di procedere.

### Ollama Cloud

Per i modelli AI, Trustable utilizza **Ollama Cloud**. È disponibile anche la versione free, che fornisce crediti gratuiti (ma limitati) per provare. Occorre registrare un account gratuito.

Se si vuole lavorare più intensamente occorre:

- Un **Ollama locale con GPU** (come le nostre BestIA), oppure
- Un **account Pro** su Ollama Cloud


È possibile che ci siano problemi su altri sistemi operativi o configurazioni.

## Installazione

### 1. Scaricare Trustable

**Windows**:

Aprire la PowerShell (premere Windows+R e scrivere 'powershell') ed eseguire

```powershell
irm n7s.co/get-trustable | iex
```

Se ci sono problemi in `ensure prerequisites` occorre disabilitare la real time protection.

Poi **chiudere e riaprire** il terminale prima di proseguire.

**Linux / Mac ** :

Aprire il terminare ed eseguire:

```bash
curl -sL n7s.co/get-trustable | bash
```

Poi **chiudere e riaprire** il terminale prima di proseguire.

### 2. Setup

Per l'installazione eseguire:

```bash
ops trustable setup
```

Se il sistema non trova ops, occorre chiudere e riaprire il terminale.

> **Nota:** L'installazione dura un certo tempo e scarica molti componenti. Se avete una rete lenta e si verifica un timeout, si può riprovare: l'installazione è incrementale e non riparte da zero.

## Utilizzo

Una volta installato, Trustable parte in automatico.

Per accedervi le volte successive usare:

```bash
ops trustable signin
```

## Risoluzione problemi

Per verificare lo stato di salute della installazione usare:

```bash
ops trustable doctor
```

Se il dottore  riscontrano problemi, provate auna semplice risoluzione con:

```bash
ops trustable restart
```

Per accedere ai log e facilitare il debugging potete ispezionare cosa succede con il comando:

```bash
ops trustable logs
```

I log sono continui. Premere Control-C per interropere la visualizzazione.

## Disinstallazione

Per disinstallare tutto:

```bash
ops trustable uninstall
```
