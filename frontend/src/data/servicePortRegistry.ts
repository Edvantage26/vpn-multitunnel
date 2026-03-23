export interface ServicePortEntry {
  service: string
  port: number
}

export const SERVICE_PORT_REGISTRY: ServicePortEntry[] = [
  // Web
  { service: "HTTP", port: 80 },
  { service: "HTTPS", port: 443 },
  { service: "HTTP-Alt", port: 8080 },
  { service: "HTTPS-Alt", port: 8443 },

  // Remote Access
  { service: "SSH", port: 22 },
  { service: "Telnet", port: 23 },
  { service: "RDP", port: 3389 },
  { service: "VNC", port: 5900 },

  // Email
  { service: "SMTP", port: 25 },
  { service: "SMTP-TLS", port: 587 },
  { service: "POP3", port: 110 },
  { service: "POP3S", port: 995 },
  { service: "IMAP", port: 143 },
  { service: "IMAPS", port: 993 },

  // File Transfer
  { service: "FTP", port: 21 },
  { service: "SFTP", port: 22 },

  // DNS & Directory
  { service: "DNS", port: 53 },
  { service: "LDAP", port: 389 },
  { service: "LDAPS", port: 636 },

  // Databases
  { service: "MySQL", port: 3306 },
  { service: "PostgreSQL", port: 5432 },
  { service: "SQL Server", port: 1433 },
  { service: "Oracle DB", port: 1521 },
  { service: "MongoDB", port: 27017 },
  { service: "Redis", port: 6379 },
  { service: "Memcached", port: 11211 },
  { service: "CouchDB", port: 5984 },
  { service: "Neo4j", port: 7474 },
  { service: "InfluxDB", port: 8086 },
  { service: "Cassandra", port: 9042 },
  { service: "ClickHouse", port: 8123 },

  // Message Queues
  { service: "RabbitMQ", port: 5672 },
  { service: "RabbitMQ Mgmt", port: 15672 },
  { service: "Kafka", port: 9092 },
  { service: "ZooKeeper", port: 2181 },
  { service: "NATS", port: 4222 },
  { service: "MQTT", port: 1883 },
  { service: "MQTTS", port: 8883 },

  // Container & Orchestration
  { service: "Docker API", port: 2376 },
  { service: "Kubernetes API", port: 6443 },
  { service: "etcd", port: 2379 },
  { service: "Kubelet", port: 10250 },

  // Monitoring & Observability
  { service: "Prometheus", port: 9090 },
  { service: "Grafana", port: 3000 },
  { service: "Elasticsearch", port: 9200 },
  { service: "Kibana", port: 5601 },
  { service: "Jaeger", port: 16686 },
  { service: "Zipkin", port: 9411 },

  // CI/CD & DevOps
  { service: "Jenkins", port: 8081 },
  { service: "GitLab", port: 8929 },
  { service: "SonarQube", port: 9000 },

  // HashiCorp
  { service: "Consul", port: 8500 },
  { service: "Vault", port: 8200 },
  { service: "Nomad", port: 4646 },

  // Storage
  { service: "MinIO", port: 9000 },
  { service: "MinIO Console", port: 9001 },

  // Other
  { service: "Syslog", port: 514 },
  { service: "SNMP", port: 161 },
  { service: "NTP", port: 123 },
  { service: "SMB", port: 445 },
  { service: "Proxy (SOCKS)", port: 1080 },
  { service: "HTTP Proxy", port: 3128 },
]

const portToServiceMap = new Map<number, ServicePortEntry>(
  SERVICE_PORT_REGISTRY.map(entry => [entry.port, entry])
)

export function getServiceByPort(portNumber: number): ServicePortEntry | undefined {
  return portToServiceMap.get(portNumber)
}

export function formatPortLabel(portNumber: number): string {
  const entry = portToServiceMap.get(portNumber)
  if (entry) {
    return `${entry.service}(${portNumber})`
  }
  return String(portNumber)
}

export function searchServices(query: string, excludedPorts: number[]): ServicePortEntry[] {
  const lowerQuery = query.toLowerCase().trim()
  if (!lowerQuery) return []

  const excludedSet = new Set(excludedPorts)
  const numericQuery = parseInt(lowerQuery, 10)
  const isNumericQuery = !isNaN(numericQuery)

  const matchingEntries = SERVICE_PORT_REGISTRY.filter(entry => {
    if (excludedSet.has(entry.port)) return false
    const matchesName = entry.service.toLowerCase().includes(lowerQuery)
    const matchesPort = isNumericQuery && String(entry.port).includes(lowerQuery)
    return matchesName || matchesPort
  })

  // Sort: exact port match first, then alphabetical by service name
  matchingEntries.sort((entryA, entryB) => {
    if (isNumericQuery) {
      const entryAExact = entryA.port === numericQuery
      const entryBExact = entryB.port === numericQuery
      if (entryAExact && !entryBExact) return -1
      if (!entryAExact && entryBExact) return 1
    }
    return entryA.service.localeCompare(entryB.service)
  })

  return matchingEntries.slice(0, 8)
}
