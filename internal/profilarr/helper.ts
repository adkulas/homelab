// Private compatibility contract for the exact image in bootstrap.go. No imports,
// application migrations, or shell interpolation. Credentials arrive only on stdin.
const input = JSON.parse(await new Response(Deno.stdin.readable).text());
const encoder = new TextEncoder();
const cstring = (text: string) => encoder.encode(text + '\0');
const sqlite = Deno.dlopen('/usr/lib/libsqlite3.so.0', {
  sqlite3_open_v2: { parameters: ['buffer', 'buffer', 'i32', 'pointer'], result: 'i32' },
  sqlite3_close: { parameters: ['pointer'], result: 'i32' },
  sqlite3_prepare_v2: { parameters: ['pointer', 'buffer', 'i32', 'buffer', 'pointer'], result: 'i32' },
  sqlite3_bind_text: { parameters: ['pointer', 'i32', 'buffer', 'i32', 'pointer'], result: 'i32' },
  sqlite3_bind_int64: { parameters: ['pointer', 'i32', 'i64'], result: 'i32' },
  sqlite3_bind_null: { parameters: ['pointer', 'i32'], result: 'i32' },
  sqlite3_step: { parameters: ['pointer'], result: 'i32' },
  sqlite3_finalize: { parameters: ['pointer'], result: 'i32' },
  sqlite3_column_count: { parameters: ['pointer'], result: 'i32' },
  sqlite3_column_type: { parameters: ['pointer', 'i32'], result: 'i32' },
  sqlite3_column_int64: { parameters: ['pointer', 'i32'], result: 'i64' },
  sqlite3_column_double: { parameters: ['pointer', 'i32'], result: 'f64' },
  sqlite3_column_text: { parameters: ['pointer', 'i32'], result: 'pointer' },
});
const api = sqlite.symbols;
let db: Deno.PointerValue = null;
const fail = (code: string): never => { throw new Error(code); };
function open(write = false) {
  const out = new BigUint64Array(1);
  if (api.sqlite3_open_v2(cstring('/config/data/profilarr.db'), out, write ? 2 : 1, null) !== 0) fail('migration_failure');
  db = Deno.UnsafePointer.create(out[0]);
}
function close() { if (db) { if (api.sqlite3_close(db) !== 0) fail('mutation_failure'); db = null; } }
function sql(text: string, params: (string | number | null)[] = []): any[][] {
  const out = new BigUint64Array(1);
  if (api.sqlite3_prepare_v2(db, cstring(text), -1, out, null) !== 0) fail('mutation_failure');
  const stmt = Deno.UnsafePointer.create(out[0]);
  try {
    params.forEach((value, i) => {
      let result: number;
      if (value === null) result = api.sqlite3_bind_null(stmt, i + 1);
      else if (typeof value === 'number') result = api.sqlite3_bind_int64(stmt, i + 1, BigInt(value));
      else { const buf = cstring(value); result = api.sqlite3_bind_text(stmt, i + 1, buf, buf.length - 1, Deno.UnsafePointer.create(0xffffffffffffffffn)); }
      if (result !== 0) fail('mutation_failure');
    });
    const rows: any[][] = [];
    while (true) {
      const result = api.sqlite3_step(stmt);
      if (result === 101) break;
      if (result !== 100) fail('mutation_failure');
      rows.push(Array.from({ length: api.sqlite3_column_count(stmt) }, (_, i) => {
        switch (api.sqlite3_column_type(stmt, i)) {
          case 1: return Number(api.sqlite3_column_int64(stmt, i));
          case 2: return api.sqlite3_column_double(stmt, i);
          case 3: return new Deno.UnsafePointerView(api.sqlite3_column_text(stmt, i)!).getCString();
          case 5: return null;
          default: return fail('unsupported_schema');
        }
      }));
    }
    return rows;
  } finally { api.sqlite3_finalize(stmt); }
}
async function hash(rows: any[][]) {
  const bytes = new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(JSON.stringify(rows))));
  return Array.from(bytes, v => v.toString(16).padStart(2, '0')).join('');
}
async function compatible() {
  if (await hash(sql("SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name")) !== 'd72dbb6d11713392c760ce34cd463770df6b6da9044ec8ddbcc45dffbd43d35f' ||
      await hash(sql('SELECT version,name FROM migrations ORDER BY version')) !== '1fee31359d63969ca3eb7d98ba06ee535a878a4330fab480efa720296324ba4b') fail('unsupported_schema');
  if (JSON.stringify(sql('PRAGMA integrity_check')) !== '[["ok"]]' || sql('PRAGMA foreign_key_check').length) fail('non_fresh_state');
}
async function command(program: string, args: string[]) {
  const result = await new Deno.Command(program, { args, stdout: 'piped', stderr: 'piped' }).output();
  if (!result.success) fail('mutation_failure');
  return new TextDecoder().decode(result.stdout).trim();
}
async function fresh() {
  for (const [table] of sql("SELECT name FROM sqlite_schema WHERE type='table' AND (name LIKE 'arr_%' OR name IN ('upgrade_configs','upgrade_runs','rename_runs'))")) {
    if (sql(`SELECT count(*) FROM "${table}"`)[0][0]) fail('non_fresh_state');
  }
  for (const table of ['job_queue','job_run_history']) {
    if (sql(`SELECT count(*) FROM ${table} WHERE job_type LIKE 'arr.%' OR (job_type LIKE 'pcd.%' AND job_type != 'pcd.sync')`)[0][0]) fail('non_fresh_state');
  }
  if (sql("SELECT count(*) FROM pcd_ops WHERE origin != 'base' OR state != 'published' OR source != 'repo'")[0][0]) fail('non_fresh_state');
  for (const table of ['pcd_ops','pcd_op_history']) {
    if (sql(`SELECT count(*) FROM ${table} WHERE database_id NOT IN (SELECT id FROM database_instances)`)[0][0]) fail('non_fresh_state');
  }
  const sourceIds = sql('SELECT id FROM database_instances');
  for (const [source,payload] of sql("SELECT source,payload FROM job_queue WHERE job_type='pcd.sync'")) {
    let data;
    try { data = JSON.parse(payload); } catch { fail('non_fresh_state'); }
    if (data === null || typeof data !== 'object' || Array.isArray(data)) fail('non_fresh_state');
    if (source !== 'schedule' || sourceIds.length !== 1 || data.databaseId !== sourceIds[0][0] || Object.keys(data).length !== 1) fail('non_fresh_state');
  }
  const sources = sql('SELECT uuid,name,repository_url,local_path,sync_strategy,auto_pull,enabled,personal_access_token,is_private,local_ops_enabled,git_user_name,git_user_email,conflict_strategy,created_at=updated_at FROM database_instances');
  if (sources.length > 1) fail('non_fresh_state');
  if (sources.length) {
    const [uuid,name,url,path,strategy,pull,enabled,pat,priv,local,gitname,gitemail,conflict,untouched] = sources[0];
    if (!/^[a-f0-9-]{36}$/.test(uuid) || name !== 'Dictionarry' || url !== 'https://github.com/Dictionarry-Hub/database' ||
        path !== `/config/data/databases/${uuid}` || strategy !== 60 || pull !== 1 || enabled !== 1 || pat || priv || local || gitname || gitemail || conflict !== 'override' || untouched !== 1) fail('non_fresh_state');
    // Imported base operations must still match the clean source checkout.
    for (const [filename,storedSql,storedMetadata] of sql('SELECT filename,sql,metadata FROM pcd_ops')) {
      if (typeof filename !== 'string' || filename.includes('/') || filename.includes('\\')) fail('non_fresh_state');
      const raw = await Deno.readTextFile(path + '/ops/' + filename);
      const metadata: Record<string,string> = {};
      const cleaned: string[] = [];
      for (const line of raw.split(/\r?\n/)) {
        const match = line.match(/^--\s*@([a-zA-Z_]+)\s*:\s*(.*)$/);
        if (match) metadata[match[1]] = match[2].trim(); else cleaned.push(line);
      }
      const expectedMetadata = metadata.operation && metadata.entity && metadata.name ? JSON.stringify(metadata) : null;
      if (cleaned.join('\n').trim() !== storedSql || expectedMetadata !== storedMetadata) fail('non_fresh_state');
    }
    // A failed first clone is also a stock fresh state. An existing checkout must be clean.
    try {
      await Deno.stat(path + '/.git');
      const status = await command('git', ['--no-optional-locks', '-c', 'core.fsmonitor=false', '-c', 'safe.directory=' + path, '-C', path, 'status', '--porcelain']);
      if (status && status !== '?? deps/') fail('non_fresh_state');
      if (status) {
        const entries = []; for await (const entry of Deno.readDir(path + '/deps')) entries.push(entry.name);
        if (entries.length !== 1 || entries[0] !== 'schema') fail('non_fresh_state');
        const dep = path + '/deps/schema';
        const git = ['--no-optional-locks','-c','core.fsmonitor=false','-c','safe.directory=' + dep,'-C',dep];
        for await (const entry of Deno.readDir(dep)) if (!['.git','ops','pcd.json'].includes(entry.name)) fail('non_fresh_state');
        if (await command('git',[...git,'status','--porcelain','--','ops','pcd.json']) || await command('git',[...git,'remote','get-url','origin']) !== 'https://github.com/Dictionarry-Hub/schema') fail('non_fresh_state');
      }
    }
    catch (error) { if (!(error instanceof Deno.errors.NotFound)) throw error; }
  }
  // A completed/attempted first-start source link proves startup passed migrations.
  if (JSON.stringify(sql('SELECT default_database_linked FROM setup_state')) !== '[[1]]') fail('migration_failure');
}
const archive = '/backup/' + input.archive;
try {
  if (!/^bootstrap-[a-f0-9-]{36}\.tar$/.test(input.archive)) fail('mutation_failure');
  if (input.mode === 'restore') {
    // Verify archive readability before removing the failed state.
    await command('tar', ['-tf', archive]);
    for await (const entry of Deno.readDir('/config')) await Deno.remove('/config/' + entry.name, { recursive: true });
    await command('tar', ['-xpf', archive, '-C', '/config']);
  } else if (input.mode === 'backup') {
    await command('tar', ['-cpf', archive, '-C', '/config', '.']);
    await Deno.chmod(archive, 0o600);
    const backupOwner = await Deno.stat("/backup");
    await Deno.chown(archive, backupOwner.uid, backupOwner.gid);
    await command('tar', ['-tf', archive]);
    const archiveFile = await Deno.open(archive, {read:true});
    try { await archiveFile.sync(); } finally { archiveFile.close(); }
  } else {
    open(input.mode === 'bootstrap');
    await compatible();
    await fresh();
    if (input.mode === 'bootstrap') {
      const path = '/config/data/databases/' + input.uuid;
      if (!/^[a-f0-9-]{36}$/.test(input.uuid) || input.revision !== '9e424382191de7d507efc9806ac3c807793d1c60') fail('unsupported_policy');
      await command('git', ['init', path]);
      await command('git', ['-C', path, 'remote', 'add', 'origin', 'https://github.com/Dictionarry-Hub/trash-pcd']);
      await command('git', ['-C', path, 'fetch', '--depth=1', 'origin', input.revision]);
      await command('git', ['-C', path, 'checkout', '--detach', 'FETCH_HEAD']);
      if (await command('git', ['-C', path, 'rev-parse', 'HEAD']) !== input.revision) fail('unsupported_policy');
      const owner = await Deno.stat('/config/data/profilarr.db');
      await command('chown', ['-R', `${owner.uid}:${owner.gid}`, path]);
      sql('PRAGMA foreign_keys=ON'); sql('PRAGMA busy_timeout=5000'); sql('BEGIN IMMEDIATE');
      try {
        // Recheck inside the transaction before changing private application state.
        await fresh();
        sql('DELETE FROM pcd_op_history'); sql('DELETE FROM pcd_ops');
        sql("DELETE FROM job_queue WHERE job_type='pcd.sync'");
        sql('DELETE FROM database_instances');
        sql('INSERT INTO database_instances(uuid,name,repository_url,local_path,sync_strategy,auto_pull) VALUES(?,?,?,?,0,0)', [input.uuid,'Media Stack TRaSH','https://github.com/Dictionarry-Hub/trash-pcd',path]);
        const source = sql('SELECT last_insert_rowid()')[0][0];
        const now = new Date().toISOString();
        for (const [type,port,key,profiles,naming,definitions] of [
          ['radarr',7878,input.radarrKey,['HD Bluray + WEB','Remux 2160p (Alternative)'],'Jellyfin TMDB','Movie'],
          ['sonarr',8989,input.sonarrKey,['WEB-1080p'],'Jellyfin TVDB','Series'],
        ]) {
          if (typeof key !== 'string' || !key.length) fail('mutation_failure');
          sql('INSERT INTO arr_instances(name,type,url,api_key,enabled) VALUES(?,?,?,?,1)', [type === 'radarr' ? 'Radarr' : 'Sonarr',type,`http://${type}:${port}`,key]);
          const id = sql('SELECT last_insert_rowid()')[0][0];
          sql('INSERT INTO arr_sync_database_priority(instance_id,database_id,priority) SELECT ?,id,ROW_NUMBER() OVER(ORDER BY id) FROM database_instances', [id]);
          for (const profile of profiles) sql('INSERT INTO arr_sync_quality_profiles(instance_id,database_id,profile_name) VALUES(?,?,?)',[id,source,profile]);
          sql("INSERT INTO arr_sync_quality_profiles_config(instance_id,trigger,cron,next_run_at) VALUES(?,'schedule','0 4 * * *',?)",[id,now]);
          sql("INSERT INTO arr_sync_delay_profiles_config(instance_id,database_id,profile_name,trigger,cron,next_run_at) VALUES(?,?,'Default','schedule','0 4 * * *',?)",[id,source,now]);
          sql("INSERT INTO arr_sync_media_management(instance_id,naming_database_id,naming_config_name,quality_definitions_database_id,quality_definitions_config_name,media_settings_database_id,media_settings_config_name,trigger,cron,next_run_at) VALUES(?,?,?,?,?,?,'Default','schedule','0 4 * * *',?)",[id,source,naming,source,definitions,source,now]);
        }
        if (sql('PRAGMA foreign_key_check').length) fail('mutation_failure');
        sql('COMMIT');
      } catch (error) { sql('ROLLBACK'); throw error; }
      sql('PRAGMA wal_checkpoint(TRUNCATE)');
    }
    close();
  }
  console.log(JSON.stringify({ code: 'ok' }));
} catch (error) {
  // SQLite and subprocess errors may contain secrets. Only allow known stable codes.
  const codes = ['unsupported_schema','unsupported_policy','non_fresh_state','migration_failure','mutation_failure'];
  const code = codes.includes(error?.message) ? error.message : 'mutation_failure';
  console.log(JSON.stringify({ code }));
  Deno.exitCode = 1;
} finally { if (db) api.sqlite3_close(db); sqlite.close(); }
