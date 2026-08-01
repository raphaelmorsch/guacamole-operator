# DesktopPool

Desktop VMs for Guacamole, cloned from the golden-image DataSource
[`win2k19-guacamole-desktop`](https://github.com/raphaelmorsch/pipeline-win2k19-server-vm)
published by the Windows Server 2019 Tekton pipeline.

## Scope (MVP)

| Done now | Later |
|---|---|
| `DesktopPool` with declarative `replicas` | Autoscaling via `bufferSize` / `maxSize` |
| Clone VM from CDI `DataSource` | Calendar / scheduled power windows |
| Per-VM RDP `Service` | Guest hibernate (only KubeVirt Halted today) |
| TCP readiness on `:3389` | KEDA-driven pool sizing |
| `DesktopSession` exclusive allocation + GuacamoleConnection | |
| Power management (idle stop + wake on demand) | |
| Desktop Portal (Keycloak users + batch sessions) | |

## Flow

```
Golden Image (win2k19-guacamole-desktop)
        ↓
   DesktopPool CR
        ↓
 DesktopPoolReconciler
        ↓
 VMs clonadas + Service RDP
        ↓
 RDP TCP ready → state=Available
        ↓
 GuacamoleConnection (MVP)
```

## Example

```yaml
apiVersion: guacamole.guacamole.io/v1alpha1
kind: DesktopPool
metadata:
  name: windows-desktop
  namespace: guacamole-desktops
spec:
  replicas: 2
  source:
    dataSource:
      name: win2k19-guacamole-desktop
      namespace: win2k19-golden
  virtualMachine:
    storageClassName: ocs-external-storagecluster-ceph-rbd-immediate
    diskSize: 40Gi
    cpu: 2
    memory: 4Gi
  network:
    rdpPort: 3389
  guacamole:
    instanceRef:
      name: guacamole
      namespace: guacamole
    username: Administrator
    # Lab: operator creates Secret <pool>-rdp-credentials
    password: "ChangeMe!"
    # Production alternative (existing Secret; operator does not create one):
    # passwordSecretRef:
    #   name: windows-desktop-credentials
    #   key: password
  recyclePolicy: Delete
  createConnections: true
  powerManagement:
    enabled: true
    idleSeconds: 900   # 15 minutes
  # minReady: 0        # keep this many warm Available desktops
```

## Power management

When `spec.powerManagement` is set (sample enables it by default):

| Behavior | Detail |
|---|---|
| Idle stop | `Available` desktops older than `idleSeconds` (default **900**) are stopped (`runStrategy: Halted`, state `Stopped`) |
| Warm floor | At least `minReady` (default **0**) Available desktops stay running |
| Wake on demand | Broker waiters (`DesktopSession` Pending/Queued) wake Stopped VMs → `Booting` → RDP ready → `Available` |
| Manual | Portal / annotation `desktop.guacamole.io/power-request=wake\|suspend` |
| Allocated | Never stopped |

Status fields: `status.stopped`, condition `PowerManagement`, member state `Stopped`.

Idle clock annotation: `desktop.guacamole.io/available-since` (set when a desktop becomes Available).

Omit `spec.powerManagement` entirely to keep the always-on warm pool (disabled).

### Credentials

| Campo | Comportamento |
|---|---|
| `guacamole.password` | Cria/atualiza Secret `{pool}-rdp-credentials` (ou `credentialsSecretName`) com `username`/`password` |
| `guacamole.passwordSecretRef` | Usa Secret existente; não cria Secret |
| Precedência | `passwordSecretRef` ganha se ambos forem setados |

`status.credentialsSecret` e condition `CredentialsReady` reportam o Secret em uso.

## CDI clone RBAC

When `spec.source.dataSource.namespace` differs from the DesktopPool namespace, the
controller automatically creates:

| Resource | Where | Purpose |
|---|---|---|
| ServiceAccount `guacamole-desktop-pool` | pool namespace | Used by desktop VMs |
| Role `guacamole-desktop-cloner` | golden-image namespace | `get` PVC + `create` pods + read DataSource/DV |
| RoleBinding `guacamole-desktop-cloner-<pool-ns>` | golden-image namespace | Binds the pool SA (and `default`) |

Status condition `CloneAuthorized=True` means clone RBAC is ready.
On DesktopPool delete, the RoleBinding is removed when no other pool in that
namespace still needs the golden image.

## Labels

| Label | Purpose |
|---|---|
| `desktop.guacamole.io/pool` | Pool ownership |
| `desktop.guacamole.io/state` | `Provisioning` / `Booting` / `Available` / `Allocated` / `Stopped` / `Failed` |
| `desktop.guacamole.io/session` | DesktopSession that reserved the VM |
| `desktop.guacamole.io/requester` | Subject that requested the session |
| `desktop.guacamole.io/managed-by` | `guacamole-operator` |
| `desktop.guacamole.io/vm` | Links Service/Connection to a VM |

## Readiness

A desktop becomes `Available` only when:

1. VMI phase is `Running`
2. VMI condition `Ready=True`
3. RDP Service has Endpoints
4. TCP connect to `{vm}-rdp.{ns}.svc:{rdpPort}` succeeds

Probe interface: `internal/readiness.DesktopReadinessProber` (fakeable in tests).

## DesktopSession

With `createConnections: false`, connections appear only when a session allocates a VM:

```yaml
apiVersion: guacamole.guacamole.io/v1alpha1
kind: DesktopSession
metadata:
  name: desktop-session-usuario1
  namespace: guacamole-desktops
spec:
  poolRef:
    name: windows-desktop
  requester:
    subject: usuario1
  # ttlSecondsAfterReady: 3600
```

Flow:

```text
Pending → reserve Available VM (label state=Allocated)
       → create GuacamoleConnection (owned by session, READ for requester.subject)
       → Ready
delete/TTL → remove connection → Delete VM (recyclePolicy)
           → DesktopPool replenishes Available buffer
```

```bash
oc apply -f config/samples/guacamole_v1alpha1_desktopsession.yaml
oc get desktopsession -n guacamole-desktops -w
# when Ready, open Guacamole — connection named after the session appears
oc delete desktopsession desktop-session-usuario1 -n guacamole-desktops
```

## Success criteria (MVP)

1. Create `DesktopPool` with `replicas: 2`
2. Operator clones two VMs from the DataSource
3. Both VMs start
4. Operator creates two RDP Services
5. Ports 3389 become reachable
6. Status shows `available=2`
7. Reduce `replicas` to `1` → one Available VM removed
8. Delete the DesktopPool → no orphan VM / PVC / Service / Secret / Connection
