# DesktopPool

Desktop VMs for Guacamole, cloned from the golden-image DataSource
[`win2k19-guacamole-desktop`](https://github.com/raphaelmorsch/pipeline-win2k19-server-vm)
published by the Windows Server 2019 Tekton pipeline.

## Scope (MVP)

| Done now | Later |
|---|---|
| `DesktopPool` with declarative `replicas` | Autoscaling via `minReady` / `bufferSize` / `maxSize` |
| Clone VM from CDI `DataSource` | `DesktopSession` exclusive allocation |
| Per-VM RDP `Service` | Portal de solicitação |
| TCP readiness on `:3389` | KEDA-driven pool sizing |
| Provisional `GuacamoleConnection` per Available VM | Connection only on session reserve |

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
```

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
| `desktop.guacamole.io/state` | `Provisioning` / `Booting` / `Available` / `Allocated` / `Failed` |
| `desktop.guacamole.io/managed-by` | `guacamole-operator` |
| `desktop.guacamole.io/vm` | Links Service/Connection to a VM |

## Readiness

A desktop becomes `Available` only when:

1. VMI phase is `Running`
2. VMI condition `Ready=True`
3. RDP Service has Endpoints
4. TCP connect to `{vm}-rdp.{ns}.svc:{rdpPort}` succeeds

Probe interface: `internal/readiness.DesktopReadinessProber` (fakeable in tests).

## DesktopSession (next)

```yaml
apiVersion: guacamole.guacamole.io/v1alpha1
kind: DesktopSession
metadata:
  generateName: desktop-session-
spec:
  poolRef:
    name: windows-desktop
  requester:
    subject: usuario1
```

When allocation is enabled: Pending → reserve Available VM → Allocated → create
`GuacamoleConnection` → Ready. On delete with `recyclePolicy: Delete`, the VM is
removed and the pool replenishes.

## Success criteria (MVP)

1. Create `DesktopPool` with `replicas: 2`
2. Operator clones two VMs from the DataSource
3. Both VMs start
4. Operator creates two RDP Services
5. Ports 3389 become reachable
6. Status shows `available=2`
7. Reduce `replicas` to `1` → one Available VM removed
8. Delete the DesktopPool → no orphan VM / PVC / Service / Secret / Connection
