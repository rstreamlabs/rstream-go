# See LICENSE file in the project root for license information.
param(
  [Parameter(Mandatory = $true)]
  [string] $Rstream,
  [string] $CppServer = "",
  [string] $CppClient = ""
)

$ErrorActionPreference = "Stop"
$Root = Join-Path $env:TEMP ("rstream-webtty-runtime-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Root | Out-Null
$Processes = New-Object System.Collections.Generic.List[object]
$Pass = 0
$Fail = 0

function Write-Pass {
  param([string] $Name)
  Write-Output ("PASS {0}" -f $Name)
  $script:Pass++
}

function Write-Fail {
  param([string] $Name, [string] $Message)
  Write-Output ("FAIL {0} {1}" -f $Name, $Message)
  $script:Fail++
}

function Get-FreePort {
  $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
  $listener.Start()
  $port = $listener.LocalEndpoint.Port
  $listener.Stop()
  return $port
}

function Wait-Tcp {
  param([string] $Address)
  $parts = $Address.Split(":")
  $hostName = $parts[0]
  $port = [int] $parts[1]
  $deadline = (Get-Date).AddSeconds(20)
  while ((Get-Date) -lt $deadline) {
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
      $task = $client.ConnectAsync($hostName, $port)
      if ($task.Wait(500) -and $client.Connected) {
        $client.Dispose()
        return
      }
    } catch {
      Write-Debug ("TCP probe failed: {0}" -f $_.Exception.Message)
    } finally {
      $client.Dispose()
    }
    Start-Sleep -Milliseconds 200
  }
  throw "timeout waiting for $Address"
}

function Invoke-WebTTYProcess {
  param([string] $Name, [string] $Executable, [string[]] $ServerArgs)
  $stdout = Join-Path $Root "$Name.out.log"
  $stderr = Join-Path $Root "$Name.err.log"
  $cleanArgs = @($ServerArgs | Where-Object { $null -ne $_ -and $_ -ne "" })
  $process = Start-Process -FilePath $Executable -ArgumentList $cleanArgs -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru -WindowStyle Hidden
  $script:Processes.Add([pscustomobject]@{ Process = $process; Stdout = $stdout; Stderr = $stderr }) | Out-Null
  return [pscustomobject]@{ Process = $process; Stdout = $stdout; Stderr = $stderr }
}

function Invoke-WebTTYServer {
  param([string] $Name, [string[]] $ServerArgs)
  Invoke-WebTTYProcess $Name $Rstream $ServerArgs
}

function Invoke-Case {
  param([string] $Name, [scriptblock] $Body)
  try {
    & $Body
    Write-Pass $Name
  } catch {
    Write-Fail $Name $_.Exception.Message
  }
}

function Invoke-ExpectFail {
  param([string] $Name, [string] $Expected, [scriptblock] $Body)
  $output = @()
  try {
    $global:LASTEXITCODE = 0
    $output = & $Body 2>&1
    if ($LASTEXITCODE -eq 0) {
      Write-Fail $Name ("command succeeded unexpectedly: " + ($output -join " "))
      return
    }
    $message = $output -join " "
  } catch {
    $message = $_.Exception.Message
  }
  if ($message -like "*$Expected*") {
    Write-Pass $Name
  } else {
    Write-Fail $Name $message
  }
}

function Assert-ContainsText {
  param([string] $Value, [string] $Needle)
  if ($Value -notlike "*$Needle*") {
    throw "expected output to contain '$Needle', got: $Value"
  }
}

function Invoke-WithHome {
  param([string] $HomePath, [scriptblock] $Body)
  $oldHome = $env:HOME
  $oldUserProfile = $env:USERPROFILE
  try {
    $env:HOME = $HomePath
    $env:USERPROFILE = $HomePath
    & $Body
  } finally {
    $env:HOME = $oldHome
    $env:USERPROFILE = $oldUserProfile
  }
}

function Write-WebTTYCertificate {
  param([string] $CertFile, [string] $KeyFile)
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "go is required to generate the local WebTTY test certificate"
  }
  $source = Join-Path $Root "make-webtty-cert.go"
  @'
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: make-webtty-cert <cert> <key>")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	certFile, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		panic(err)
	}
	keyFile, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		panic(err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		panic(err)
	}
}
'@ | Set-Content -Encoding utf8 $source
  $output = & go run $source $CertFile $KeyFile 2>&1
  if ($LASTEXITCODE -ne 0) {
    throw "certificate generation failed: $($output -join ' ')"
  }
}

try {
  & $Rstream --version | Out-Host

  $wsPort = Get-FreePort
  $wsAddr = "127.0.0.1:$wsPort"
  Invoke-WebTTYServer "ws" @("webtty", "server", "-v", "--listen", $wsAddr, "--transport", "websocket", "--allow-unauthenticated") | Out-Null
  Wait-Tcp $wsAddr
  Invoke-Case "windows/ws/spawn/plaintext" {
    $out = & $Rstream webtty exec --url "ws://$wsAddr" --transport websocket -- powershell -NoProfile -Command "Write-Output go-ws" 2>&1
    Assert-ContainsText ($out -join "`n") "go-ws"
  }

  $plainPort = Get-FreePort
  $plainAddr = "127.0.0.1:$plainPort"
  Invoke-WebTTYServer "plain" @("webtty", "server", "-v", "--listen", $plainAddr, "--transport", "plain", "--allow-unauthenticated") | Out-Null
  Wait-Tcp $plainAddr
  Invoke-Case "windows/plain/spawn/plaintext" {
    $out = & $Rstream webtty exec --url $plainAddr --transport plain -- powershell -NoProfile -Command "Write-Output go-plain" 2>&1
    Assert-ContainsText ($out -join "`n") "go-plain"
  }

  $loginPort = Get-FreePort
  $loginAddr = "127.0.0.1:$loginPort"
  $currentUsername = [Environment]::UserName
  Invoke-WebTTYServer "login" @("webtty", "server", "-v", "--listen", $loginAddr, "--transport", "websocket", "--allow-unauthenticated", "--execution-mode", "login", "--login-user", $currentUsername) | Out-Null
  Wait-Tcp $loginAddr
  Invoke-Case "windows/ws/login-current-user" {
    $out = & $Rstream webtty exec --url "ws://$loginAddr" --transport websocket -- powershell -NoProfile -Command "Write-Output go-login" 2>&1
    Assert-ContainsText ($out -join "`n") "go-login"
  }

  $serverIdentity = Join-Path $Root "server.identity.json"
  $clientIdentity = Join-Path $Root "client.identity.json"
  $badIdentity = Join-Path $Root "bad.identity.json"
  & $Rstream webtty identity create --identity-file $serverIdentity -o json | Out-Null
  & $Rstream webtty identity create --identity-file $clientIdentity -o json | Out-Null
  & $Rstream webtty identity create --identity-file $badIdentity -o json | Out-Null
  $serverPublic = (& $Rstream webtty identity show --identity-file $serverIdentity -o json | ConvertFrom-Json)
  $clientPublic = (& $Rstream webtty identity show --identity-file $clientIdentity -o json | ConvertFrom-Json)
  $serverKnown = $serverPublic.endpoint_identity
  $clientAuthorized = "$($clientPublic.signing_key_id):$($clientPublic.signing_public_key)"

  $certFile = Join-Path $Root "webtty.crt"
  $keyFile = Join-Path $Root "webtty.key"
  Write-WebTTYCertificate $certFile $keyFile

  $plainTlsPort = Get-FreePort
  $plainTlsAddr = "127.0.0.1:$plainTlsPort"
  Invoke-WebTTYServer "plain-tls" @("webtty", "server", "-v", "--listen", $plainTlsAddr, "--transport", "plain", "--allow-unauthenticated", "--tls-cert-file", $certFile, "--tls-key-file", $keyFile) | Out-Null
  Wait-Tcp $plainTlsAddr
  Invoke-Case "windows/plain-tls/spawn/plaintext" {
    $out = & $Rstream webtty exec --url "tls://$plainTlsAddr" --transport plain --tls-ca-file $certFile -- powershell -NoProfile -Command "Write-Output go-plain-tls" 2>&1
    Assert-ContainsText ($out -join "`n") "go-plain-tls"
  }

  $plainTlsE2EIdentity = Join-Path $Root "plain-tls.identity.json"
  & $Rstream webtty identity create --identity-file $plainTlsE2EIdentity -o json | Out-Null
  $plainTlsE2EPublic = (& $Rstream webtty identity show --identity-file $plainTlsE2EIdentity -o json | ConvertFrom-Json)
  $plainTlsE2EKnown = $plainTlsE2EPublic.endpoint_identity
  $plainTlsE2EPort = Get-FreePort
  $plainTlsE2EAddr = "127.0.0.1:$plainTlsE2EPort"
  Invoke-WebTTYServer "plain-tls-e2e" @("webtty", "server", "-v", "--listen", $plainTlsE2EAddr, "--transport", "plain", "--allow-unauthenticated", "--tls-cert-file", $certFile, "--tls-key-file", $keyFile, "--e2e", "--identity-file", $plainTlsE2EIdentity, "--authorized-client-key", $clientAuthorized) | Out-Null
  Wait-Tcp $plainTlsE2EAddr
  Invoke-Case "windows/plain-tls/e2e-authorized" {
    $out = & $Rstream webtty exec --url "tls://$plainTlsE2EAddr" --transport plain --tls-ca-file $certFile --known-server-key $plainTlsE2EKnown --identity-file $clientIdentity -- powershell -NoProfile -Command "Write-Output go-plain-tls-e2e" 2>&1
    Assert-ContainsText ($out -join "`n") "go-plain-tls-e2e"
  }

  $wtPort = Get-FreePort
  $wtAddr = "127.0.0.1:$wtPort"
  Invoke-WebTTYServer "webtransport" @("webtty", "server", "-v", "--listen", $wtAddr, "--transport", "webtransport", "--allow-unauthenticated", "--tls-cert-file", $certFile, "--tls-key-file", $keyFile) | Out-Null
  Start-Sleep -Milliseconds 800
  Invoke-Case "windows/webtransport/spawn/plaintext" {
    $out = & $Rstream webtty exec --url "https://$wtAddr/" --transport webtransport --tls-insecure-skip-verify -- powershell -NoProfile -Command "Write-Output go-webtransport" 2>&1
    Assert-ContainsText ($out -join "`n") "go-webtransport"
  }

  $wtE2EIdentity = Join-Path $Root "webtransport.identity.json"
  & $Rstream webtty identity create --identity-file $wtE2EIdentity -o json | Out-Null
  $wtE2EPublic = (& $Rstream webtty identity show --identity-file $wtE2EIdentity -o json | ConvertFrom-Json)
  $wtE2EKnown = $wtE2EPublic.endpoint_identity
  $wtE2EPort = Get-FreePort
  $wtE2EAddr = "127.0.0.1:$wtE2EPort"
  Invoke-WebTTYServer "webtransport-e2e" @("webtty", "server", "-v", "--listen", $wtE2EAddr, "--transport", "webtransport", "--allow-unauthenticated", "--tls-cert-file", $certFile, "--tls-key-file", $keyFile, "--e2e", "--identity-file", $wtE2EIdentity, "--authorized-client-key", $clientAuthorized) | Out-Null
  Start-Sleep -Milliseconds 800
  Invoke-Case "windows/webtransport/e2e-authorized" {
    $out = & $Rstream webtty exec --url "https://$wtE2EAddr/" --transport webtransport --tls-insecure-skip-verify --known-server-key $wtE2EKnown --identity-file $clientIdentity -- powershell -NoProfile -Command "Write-Output go-webtransport-e2e" 2>&1
    Assert-ContainsText ($out -join "`n") "go-webtransport-e2e"
  }

  $e2ePort = Get-FreePort
  $e2eAddr = "127.0.0.1:$e2ePort"
  $e2eServer = Invoke-WebTTYServer "e2e" @("webtty", "server", "-v", "--listen", $e2eAddr, "--transport", "websocket", "--allow-unauthenticated", "--e2e", "--identity-file", $serverIdentity, "--authorized-client-key", $clientAuthorized)
  Wait-Tcp $e2eAddr
  Invoke-Case "windows/ws/e2e-authorized" {
    $out = & $Rstream webtty exec --url "ws://$e2eAddr" --transport websocket --e2e --identity-file $clientIdentity --known-server-key $serverKnown -- powershell -NoProfile -Command "Write-Output go-e2e" 2>&1
    Assert-ContainsText ($out -join "`n") "go-e2e"
  }
  Invoke-ExpectFail "windows/ws/e2e-unauthorized" "not authorized" {
    & $Rstream webtty exec --url "ws://$e2eAddr" --transport websocket --e2e --identity-file $badIdentity --known-server-key $serverKnown -- powershell -NoProfile -Command "Write-Output no"
  }
  Start-Sleep -Milliseconds 500
  $authLogCount = 0
  $authLogText = ""
  if (Test-Path $e2eServer.Stdout) {
    $authLogText += (Get-Content -Raw $e2eServer.Stdout)
  }
  if (Test-Path $e2eServer.Stderr) {
    $authLogText += "`n"
    $authLogText += (Get-Content -Raw $e2eServer.Stderr)
  }
  $authLogCount = ([regex]::Matches($authLogText, "WebTTY client signing key is not authorized")).Count
  if ($authLogCount -gt 1) {
    throw "expected at most one unauthorized server log entry, got $authLogCount"
  }
  Write-Pass "windows/ws/e2e-unauthorized-log-not-duplicated"

  if ($CppServer -ne "" -and $CppClient -ne "" -and (Test-Path $CppServer) -and (Test-Path $CppClient)) {
    & $CppServer --version | Out-Host
    & $CppClient --version | Out-Host

    $cppWsPort = Get-FreePort
    $cppWsAddr = "127.0.0.1:$cppWsPort"
    Invoke-WebTTYProcess "cpp-ws" $CppServer @("--uri=$cppWsAddr", "--transport=websocket", "--allow-unauthenticated") | Out-Null
    Wait-Tcp $cppWsAddr
    Invoke-Case "windows/cpp-server/ws/plaintext" {
      $out = & $Rstream webtty exec --url "ws://$cppWsAddr" --transport websocket -- powershell -NoProfile -Command "Write-Output cpp-server-ws" 2>&1
      Assert-ContainsText ($out -join "`n") "cpp-server-ws"
    }
    Invoke-Case "windows/cpp-client/cpp-server/ws/plaintext" {
      $out = & $CppClient "--uri=$cppWsAddr" "--transport=websocket" -I -T -- powershell -NoProfile -Command "Write-Output cpp-client-ws" 2>&1
      Assert-ContainsText ($out -join "`n") "cpp-client-ws"
    }

    $goForCppPort = Get-FreePort
    $goForCppAddr = "127.0.0.1:$goForCppPort"
    Invoke-WebTTYServer "go-for-cpp-ws" @("webtty", "server", "-v", "--listen", $goForCppAddr, "--transport", "websocket", "--allow-unauthenticated") | Out-Null
    Wait-Tcp $goForCppAddr
    Invoke-Case "windows/cpp-client/go-server/ws/plaintext" {
      $out = & $CppClient "--uri=$goForCppAddr" "--transport=websocket" -I -T -- powershell -NoProfile -Command "Write-Output go-server-ws" 2>&1
      Assert-ContainsText ($out -join "`n") "go-server-ws"
    }

    $cppE2EPort = Get-FreePort
    $cppE2EAddr = "127.0.0.1:$cppE2EPort"
    $cppServerIdentity = Join-Path $Root "cpp-server.identity.json"
    & $Rstream webtty identity create --identity-file $cppServerIdentity -o json | Out-Null
    $cppServerPublic = (& $Rstream webtty identity show --identity-file $cppServerIdentity -o json | ConvertFrom-Json)
    $cppKnownServer = $cppServerPublic.endpoint_identity
    Invoke-WebTTYProcess "cpp-e2e" $CppServer @("--uri=$cppE2EAddr", "--transport=websocket", "--allow-unauthenticated", "--e2e", "--identity-file=$cppServerIdentity", "--authorized-client-key=$clientAuthorized") | Out-Null
    Wait-Tcp $cppE2EAddr
    Invoke-Case "windows/go-client/cpp-server/ws/e2e-authorized" {
      $out = & $Rstream webtty exec --url "ws://$cppE2EAddr" --transport websocket --e2e --identity-file $clientIdentity --known-server-key $cppKnownServer -- powershell -NoProfile -Command "Write-Output cpp-server-e2e" 2>&1
      Assert-ContainsText ($out -join "`n") "cpp-server-e2e"
    }
    Invoke-Case "windows/cpp-client/cpp-server/ws/e2e-authorized" {
      $out = & $CppClient "--uri=$cppE2EAddr" "--transport=websocket" "--known-server-key=$cppKnownServer" "--identity-file=$clientIdentity" -I -T -- powershell -NoProfile -Command "Write-Output cpp-client-e2e" 2>&1
      Assert-ContainsText ($out -join "`n") "cpp-client-e2e"
    }

    $goForCppE2EPort = Get-FreePort
    $goForCppE2EAddr = "127.0.0.1:$goForCppE2EPort"
    $goForCppIdentity = Join-Path $Root "go-for-cpp.identity.json"
    Invoke-WebTTYServer "go-for-cpp-e2e" @("webtty", "server", "-v", "--listen", $goForCppE2EAddr, "--transport", "websocket", "--allow-unauthenticated", "--e2e", "--identity-file", $goForCppIdentity, "--authorized-client-key", $clientAuthorized) | Out-Null
    Wait-Tcp $goForCppE2EAddr
    $goForCppPublic = (& $Rstream webtty identity show --identity-file $goForCppIdentity -o json | ConvertFrom-Json)
    $goForCppKnownServer = $goForCppPublic.endpoint_identity
    Invoke-ExpectFail "windows/cpp-client/go-server/ws/e2e-unauthorized" "not authorized" {
      & $CppClient "--uri=$goForCppE2EAddr" "--transport=websocket" "--known-server-key=$goForCppKnownServer" "--identity-file=$badIdentity" -I -T -- powershell -NoProfile -Command "Write-Output no"
    }
    Invoke-Case "windows/cpp-client/go-server/ws/e2e-authorized" {
      $out = & $CppClient "--uri=$goForCppE2EAddr" "--transport=websocket" "--known-server-key=$goForCppKnownServer" "--identity-file=$clientIdentity" -I -T -- powershell -NoProfile -Command "Write-Output go-server-e2e" 2>&1
      Assert-ContainsText ($out -join "`n") "go-server-e2e"
    }

    $cppHome = Join-Path $Root "cpp-home"
    $cppIdentityDir = Join-Path $cppHome ".rstream\webtty\identities"
    $cppWebTTYDir = Join-Path $cppHome ".rstream\webtty"
    New-Item -ItemType Directory -Force -Path $cppIdentityDir | Out-Null
    Copy-Item -Force $clientIdentity (Join-Path $cppIdentityDir "runtime-client.identity.json")
    $knownServers = @{
      version = 1
      crypto_suite = "webtty-e2e-x25519-hpke-aes-256-gcm-v1"
      known_servers = @(@{
        name = "go-for-cpp"
        key_id = $goForCppPublic.encryption_key_id
        public_key = $goForCppPublic.encryption_public_key
        signing_key_id = $goForCppPublic.signing_key_id
        signing_public_key = $goForCppPublic.signing_public_key
        client_identity = "runtime-client"
      })
    }
    $knownServers | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $cppWebTTYDir "known_servers.json")
    Invoke-Case "windows/cpp-client/go-server/ws/e2e-known-server-client-identity" {
      Invoke-WithHome $cppHome {
        $out = & $CppClient "--uri=$goForCppE2EAddr" "--transport=websocket" "--e2e" "--known-server=go-for-cpp" -I -T -- powershell -NoProfile -Command "Write-Output go-server-known-identity" 2>&1
        Assert-ContainsText ($out -join "`n") "go-server-known-identity"
      }
    }
  } else {
    Write-Output "SKIP windows/c++ runtime C++ WebTTY binaries not provided"
  }

  Write-Output ("SUMMARY PASS {0} FAIL {1}" -f $Pass, $Fail)
  if ($Fail -ne 0) {
    exit 1
  }
} finally {
  foreach ($item in $Processes) {
    try {
      if (-not $item.Process.HasExited) {
        Stop-Process -Id $item.Process.Id -Force -ErrorAction SilentlyContinue
      }
    } catch {
      Write-Debug ("Unable to stop test process {0}: {1}" -f $item.Process.Id, $_.Exception.Message)
    }
  }
  Remove-Item -Recurse -Force $Root -ErrorAction SilentlyContinue
}
