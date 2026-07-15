# Trustable runtime architectures — draft for validation

> Status: draft di lavoro, non ancora integrato nelle specifiche normative o
> nelle istruzioni di Trustable Code.

Il termine **produzione** in questo documento indica come viene installato ed
eseguito Trustable. Non indica necessariamente che l'applicazione creata
dall'utente sia già stata pubblicata.

## Invarianti comuni

In tutte e tre le modalità:

- Trustable espone:
  - UI e API sulla porta `8910`;
  - Trustable Code/OpenCode sulla porta `4096`;
  - Vite, avviato tramite `ops ide devel`, sulla porta `5173`.
- Le action OpenServerless vengono eseguite in pod separati dal runtime
  Trustable.
- `localhost` dentro una action indica esclusivamente il container della
  action.
- Le action accedono a Redis, MongoDB, Postgres e S3 tramite i binding generati
  in `ctx`, non tramite MCP o URL pubblici.
- Il frontend usa URL relativi come `/api/my/v1/action`.
- Una action non deve chiamare action sorelle tramite `miniops.me`,
  `vite.<domain>` o altri host browser-facing.

## 1. Sviluppo: Trustable come processo dentro la VM

Nome proposto: `vm-development`.

```text
Browser macOS
    |
    | trustable.<VM-IP>.nip.io:8910
    v
Lima VM
    |-- trustable/air :8910
    |-- trustable-code :4096
    |-- ops ide devel / Vite :5173
    `-- k3s
         |-- Nuvolaris/OpenServerless
         |-- database e storage
         `-- pod delle action
```

Caratteristiche:

- `run.sh` avvia il binario Trustable direttamente nel sistema operativo della
  VM.
- Trustable Code e Vite sono processi figli nella stessa VM, non pod.
- `localhost:4096` e `localhost:5173` sono quindi porte della VM.
- k3s gira nella stessa VM, ma Trustable è esterno al cluster.
- Trustable raggiunge k3s tramite kubeconfig, ingress locale e DNS
  `cluster.local`.
- Dentro la VM `miniops.me` può raggiungere l'ingress locale.
- Dal browser macOS `miniops.me` non è affidabile perché risolve al loopback del
  Mac. Si usano:
  - `trustable.<VM-IP>.nip.io:8910`;
  - `opencode.<VM-IP>.nip.io:8910`;
  - `vite.<VM-IP>.nip.io:8910`;
  - `<app>.<VM-IP>.nip.io:8910`.
- Trustable riscrive gli host verso i corrispondenti host canonici del cluster.
- Workspace e workbench sono directory configurate nella VM, eventualmente
  collocate sul mount VirtioFS del repository.

## 2. Produzione desktop: Trustable come pod nel k3s della VM

Nome proposto: `vm-pod-production`.

```text
Browser host
    |
    v
Gateway/proxy della VM
    |
    v
Ingress k3s nella VM
    |
    v
trustable-svc
    `-- Pod trustable
         |-- container trustable
         |    |-- server :8910
         |    |-- trustable-code :4096
         |    `-- Vite :5173
         `-- reverse-proxy sidecar :80

Altri pod:
    |-- OpenServerless
    |-- servizi dati
    `-- pod delle action
```

Caratteristiche:

- Trustable gira nello StatefulSet `trustable-0`.
- Trustable Code e Vite sono processi nel container Trustable.
- Il service `trustable-svc` espone internamente `8910`, `4096` e `5173`.
- Gli ingress instradano:
  - `trustable.<domain>` verso `8910`;
  - `opencode.<domain>` verso `4096`;
  - `vite.<domain>` verso `5173`.
- Il pod contiene un reverse-proxy sidecar sulla porta `80`.
- Nel pod Trustable, `miniops.me -> 127.0.0.1` funziona perché il sidecar
  intercetta la richiesta e la inoltra all'ingress.
- Nei pod delle action lo stesso `miniops.me -> 127.0.0.1` non funziona, perché
  non esiste il sidecar. Questa è la causa dell'errore osservato nella action
  `stack-status`.
- Lo storage persistente è montato dal filesystem della VM. Attualmente il
  manifest usa `hostPath /home/trustable/workspace`.
- `/home/trustable/workbench` viene ricondotto allo storage persistente sotto
  `workspace/workbench`.

Il browser deve entrare attraverso il gateway fornito dall'installazione
desktop, non raggiungere direttamente IP o porte dei pod.

## 3. Produzione nativa: Trustable come pod in k3s

Nome proposto: `k3s-production`.

```text
Browser/rete
    |
    v
DNS + LoadBalancer/Node IP
    |
    v
Ingress k3s
    |
    v
trustable-svc
    `-- Pod trustable
         |-- trustable :8910
         |-- trustable-code :4096
         |-- Vite :5173
         `-- reverse-proxy sidecar :80

Cluster k3s
    |-- OpenServerless
    |-- servizi Nuvolaris
    |-- pod delle action
    `-- volume persistente Trustable
```

La topologia interna è quasi uguale alla modalità 2. Cambiano il substrato e
l'ingresso:

- non esiste una VM desktop come confine esterno;
- il browser entra tramite dominio configurato, IP del nodo o load balancer;
- gli host derivano dall'`OPS_APIHOST` configurato;
- `nip.io` è solo una possibile modalità di DNS, non una regola;
- i servizi interni si raggiungono tramite DNS Kubernetes;
- lo storage deve essere un volume durevole. Il manifest attuale usa
  `hostPath`, adeguato al k3s mononodo; per un cluster multinodo servirebbe un
  PVC o un vincolo esplicito al nodo.

## Regola fondamentale per Trustable Code

La matrice che l'assistente deve conoscere è:

| Chiamante | Significato di `localhost` | Host browser-facing | Accesso ai servizi dati |
|---|---|---|---|
| Browser | computer dell'utente | sì | mai direttamente |
| Trustable in sviluppo VM | VM | tramite proxy Trustable | configurazione e DNS cluster |
| Trustable nel pod | pod Trustable | ingress o sidecar | DNS e binding |
| Action OpenServerless | pod della action | non usare per composizione | `ctx.REDIS`, `ctx.MONGODB`, `ctx.POSTGRESQL`, `ctx.S3_CLIENT` |

Per lo Stack Nuvolaris la soluzione corretta è quindi una delle seguenti:

1. Il frontend chiama quattro URL relativi `/api/my/...`.
2. Una singola action riceve tutti e quattro i binding.

Non deve essere usata questa architettura:

```text
action stack-status
    -> http://miniops.me/api/my/...
    -> altre action
```

## Punti da validare

1. Nella modalità 2, il gateway browser normativo deve essere
   `*.miniops.me` tramite proxy desktop oppure `*.<VM-IP>.nip.io`.
2. Nella modalità 3, va confermato se `hostPath` resta la scelta ufficiale
   perché k3s è sempre mononodo.
3. Va confermato che il reverse-proxy sidecar rimanga obbligatorio in entrambe
   le modalità pod.

## Riferimenti dell'implementazione corrente

- `run.sh`: ciclo di sviluppo dentro la VM.
- `start.sh`: provisioning e gateway della VM di sviluppo.
- `setup.sh`: toolchain e accesso al k3s locale.
- `olaris-bestia/trustable/sts.yaml`: StatefulSet, service, sidecar e storage.
- `olaris-bestia/trustable/nginx.conf`: proxy pod-local verso l'ingress.
- `olaris-bestia/trustable/nginx.yaml` e `traefik.yaml`: ingress Trustable,
  OpenCode e Vite.
