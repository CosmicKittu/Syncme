// End-to-end test: the phone's Kotlin core against the real Go daemon.
// Run through scripts/e2e-test.sh, which starts the daemon and relay.
import com.cosmickittu.syncme.core.*
import java.io.File
import java.io.IOException
import java.util.Random
import kotlin.system.exitProcess

class FileStore(private val f: File) : PeerStore {
    override fun load(): Peer? = if (f.exists()) Peer.fromJson(org.json.JSONObject(f.readText())) else null
    override fun save(p: Peer?) { if (p == null) f.delete() else f.writeText(p.toJson().toString()) }
}

var failures = 0
fun check(cond: Boolean, what: String) {
    if (cond) println("  ok   $what") else { println("  FAIL $what"); failures++ }
}

fun waitFor(timeoutMs: Long, cond: () -> Boolean): Boolean {
    val end = System.currentTimeMillis() + timeoutMs
    while (System.currentTimeMillis() < end) { if (cond()) return true; Thread.sleep(200) }
    return cond()
}

fun main(args: Array<String>) {
    val (token, pcDirS, phoneDirS, stateDirS) = args
    val pcPort = args[4].toInt()
    val relay = args[5]
    System.setProperty("syncme.test", "1")
    val pc = File(pcDirS); val phone = File(phoneDirS); val state = File(stateDirS)
    phone.mkdirs(); state.mkdirs()
    Connector.log = { println("    [net] $it") }
    val deviceId = Crypto.randomHex(16)
    val store = FileStore(File(state, "peer.json"))
    val listener = object : SyncListener { override fun onLog(text: String) = println("    [log] $text") }

    println("discovery")
    val found = Connector.discover(1500)
    check(found.isNotEmpty(), "PC answered discovery: $found")
    val pcInfo = found.firstOrNull() ?: run { println("cannot continue"); exitProcess(1) }

    println("pairing with wrong id is rejected by TLS pinning")
    val bogus = PairTarget("0".repeat(64), "x", listOf("127.0.0.1:$pcPort"))
    check(runCatching { SyncClient.pair(bogus, token, deviceId, "Test Phone") {} }.isFailure, "pinned key mismatch fails")

    println("pairing")
    var shownCode = ""
    val target = PairTarget(pcInfo.id, pcInfo.name, listOf("127.0.0.1:$pcPort"))
    val peer = SyncClient.pair(target, token, deviceId, "Test Phone") { shownCode = it }
    store.save(peer)
    check(shownCode.length == 6, "verification code shown: $shownCode")
    check(peer.secret.size == 32, "secret received")
    File(state, "code.txt").writeText(shownCode)

    val client = SyncClient(phone, state, store, deviceId, "Test Phone", listener)

    println("initial sync: both directions")
    File(pc, "a.txt").writeText("from pc")
    File(phone, "b.txt").writeText("from phone")
    File(pc, "same.txt").writeText("same"); File(phone, "same.txt").writeText("same")
    File(phone, "dir/sub").mkdirs(); File(phone, "dir/sub/deep.txt").writeText("deep")
    val big = ByteArray(5 * 1024 * 1024 + 123).also { Random(1).nextBytes(it) }
    File(phone, "big.bin").writeBytes(big)
    var st = client.syncOnce()
    println("    $st")
    check(File(phone, "a.txt").readText() == "from pc", "pc file arrived on phone")
    check(File(pc, "b.txt").readText() == "from phone", "phone file arrived on pc")
    check(File(pc, "dir/sub/deep.txt").readText() == "deep", "nested folders")
    check(File(pc, "big.bin").readBytes().contentEquals(big), "5 MB binary intact")

    println("no-op sync")
    st = client.syncOnce()
    check(st.changes == 0 && st.errors == 0, "nothing to do: $st")

    println("edits and deletes propagate")
    Thread.sleep(20)
    File(pc, "a.txt").writeText("pc edit")
    File(phone, "b.txt").delete()
    File(pc, "dir/sub/deep.txt").delete()
    st = client.syncOnce()
    check(File(phone, "a.txt").readText() == "pc edit", "edit pc -> phone")
    check(!File(pc, "b.txt").exists(), "phone delete -> pc")
    check(!File(phone, "dir/sub/deep.txt").exists(), "pc delete -> phone")
    check(File(phone, ".syncme-trash").walkTopDown().any { it.name == "deep.txt" }, "deleted file kept in phone trash")
    check(File(pc, ".syncme-trash").walkTopDown().any { it.name == "b.txt" }, "deleted file kept in pc trash")
    check(!File(phone, "dir").exists(), "empty folders cleaned up")

    println("conflict: both edited, newest wins, other kept")
    File(pc, "a.txt").writeText("pc conflict")
    File(pc, "a.txt").setLastModified(System.currentTimeMillis() - 60_000)
    File(phone, "a.txt").writeText("phone conflict")
    st = client.syncOnce()
    check(st.conflicts == 1, "one conflict reported")
    check(File(pc, "a.txt").readText() == "phone conflict" && File(phone, "a.txt").readText() == "phone conflict", "newer version wins on both")
    val pcCopies = pc.listFiles()!!.filter { it.name.startsWith("a.sync-conflict-") }
    val phCopies = phone.listFiles()!!.filter { it.name.startsWith("a.sync-conflict-") }
    check(pcCopies.size == 1 && pcCopies[0].readText() == "pc conflict", "conflict copy on pc")
    check(phCopies.size == 1 && phCopies[0].readText() == "pc conflict", "conflict copy on phone")
    st = client.syncOnce()
    check(st.changes == 0, "stable after conflict: $st")

    println("wrong secret is rejected")
    val badStore = FileStore(File(state, "bad.json")).also { it.save(peer.copy(secret = ByteArray(32))) }
    val bad = SyncClient(phone, state, badStore, deviceId, "Test Phone", listener)
    val r = runCatching { bad.syncOnce() }
    check(r.exceptionOrNull() is RemoteException, "auth fails: ${r.exceptionOrNull()?.message}")

    println("live mode: changes are pushed without polling")
    val t = Thread { try { client.runConnected { true } } catch (e: IOException) { println("    live ended: $e") } }
    t.isDaemon = true; t.start()
    Thread.sleep(1500)
    File(pc, "live-pc.txt").writeText("live from pc")
    check(waitFor(8000) { File(phone, "live-pc.txt").exists() }, "pc change reached phone quickly")
    File(phone, "live-phone.txt").writeText("live from phone")
    client.requestSync()
    check(waitFor(8000) { File(pc, "live-phone.txt").exists() }, "phone change reached pc after requestSync")
    File(phone, "live-watch.txt").writeText("watcher")
    check(waitFor(12000) { File(pc, "live-watch.txt").exists() }, "phone change detected by watcher")
    client.dropConnection()
    t.join(5000)

    if (relay.isNotEmpty()) {
        println("relay fallback")
        Connector.discoveryEnabled = false
        val rs = FileStore(File(state, "relay.json")).also { it.save(store.load()!!.copy(addrs = listOf("127.0.0.1:1"), lastAddr = "", public = "", relay = relay)) }
        val rc = SyncClient(phone, state, rs, deviceId, "Test Phone", object : SyncListener {
            override fun onConnected(peer: Peer, via: String, local: Boolean) = println("    connected via $via")
        })
        File(pc, "via-relay.txt").writeText("relayed")
        val ok = runCatching { rc.syncOnce() }
        check(ok.isSuccess, "sync through relay ${ok.exceptionOrNull() ?: ""}")
        check(File(phone, "via-relay.txt").exists(), "file arrived through relay")
    }

    println(if (failures == 0) "ALL PASSED" else "$failures FAILED")
    exitProcess(if (failures == 0) 0 else 1)
}
