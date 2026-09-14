package polyglot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TypeScript uses the official microsoft/TypeScript compiler under Node.
// CompilerModule may be a Node module name or an absolute typescript.js path.
type TypeScript struct {
	NodeCommand    string
	CompilerModule string
}

func (t TypeScript) executable() string {
	if t.NodeCommand != "" {
		return t.NodeCommand
	}
	if command := os.Getenv("BASHPP_NODE"); command != "" {
		return command
	}
	return "node"
}

func (t TypeScript) compilerModule() string {
	if t.CompilerModule != "" {
		return t.CompilerModule
	}
	if module := os.Getenv("BASHPP_TYPESCRIPT_MODULE"); module != "" {
		return module
	}
	return "typescript"
}

func (t TypeScript) name() string { return "TypeScript" }
func (t TypeScript) arguments(Plan) []string {
	return []string{"-e", typeScriptWorker}
}
func (t TypeScript) loadRequest(plan Plan) map[string]any {
	return map[string]any{"id": 0, "op": "load", "artifact": plan.Artifact}
}

func (t TypeScript) Analyze(ctx context.Context, source string) ([]Export, error) {
	exports, _, err := t.AnalyzeArtifact(ctx, source)
	return exports, err
}

func (t TypeScript) AnalyzeArtifact(ctx context.Context, source string) ([]Export, string, error) {
	cmd := exec.CommandContext(ctx, t.executable(), "-e", typeScriptAnalyze, t.compilerModule())
	cmd.Stdin = strings.NewReader(source)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("TypeScript runtime unavailable: %w", err)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, "", errors.New(msg)
	}
	var response struct {
		OK       bool     `json:"ok"`
		V7       bool     `json:"v7"`
		Root     string   `json:"root"`
		Exports  []Export `json:"exports"`
		Artifact string   `json:"artifact"`
		Error    string   `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, "", fmt.Errorf("invalid TypeScript analyzer response: %w", err)
	}
	if response.V7 {
		cmd = exec.CommandContext(ctx, t.executable(), "--input-type=module", "-e", typeScriptAnalyze7, response.Root)
		cmd.Stdin = strings.NewReader(source)
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, "", errors.New(msg)
		}
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			return nil, "", fmt.Errorf("invalid TypeScript 7 analyzer response: %w", err)
		}
	}
	if !response.OK {
		return nil, "", errors.New(response.Error)
	}
	return response.Exports, response.Artifact, nil
}

const typeScriptAnalyze = `
const fs = require('fs');
const cp = require('child_process');
const path = require('path');
const requested = process.argv[1] || 'typescript';
function loadCompiler(name) {
  try {
    let entry, packageFile;
    if (path.isAbsolute(name) && fs.statSync(name).isDirectory()) {
      packageFile = path.join(name, 'package.json');
      const manifest = require(packageFile);
      entry = path.join(name, manifest.main || 'lib/version.cjs');
    } else {
      entry = require.resolve(name);
      packageFile = require.resolve(name + '/package.json');
    }
    return {compiler:require(entry),root:path.dirname(packageFile)};
  }
  catch (first) {
    if (name !== 'typescript') throw new Error('official TypeScript compiler module unavailable at ' + name + ': ' + first.message);
    try {
      const root = cp.execFileSync('npm', ['root', '-g'], {encoding:'utf8'}).trim();
      const packageRoot = path.join(root, 'typescript');
      return {compiler:require(packageRoot),root:packageRoot};
    } catch (_) {
      throw new Error('official TypeScript compiler module unavailable; install it with npm install -g typescript or set BASHPP_TYPESCRIPT_MODULE');
    }
  }
}
function fail(message) { process.stdout.write(JSON.stringify({ok:false,error:message})); process.exit(0); }
function diagnostic(ts, d) {
  const message = ts.flattenDiagnosticMessageText(d.messageText, '\n');
  if (!d.file || d.start == null) return message;
  const p = d.file.getLineAndCharacterOfPosition(d.start);
  return '<bash++ typescript>:' + (p.line + 1) + ':' + (p.character + 1) + ': ' + message;
}
try {
  const loaded = loadCompiler(requested), ts = loaded.compiler;
  if (typeof ts.createProgram !== 'function') {
    process.stdout.write(JSON.stringify({v7:true,root:loaded.root})); process.exit(0);
  }
  const input = '<bash++ typescript>.ts';
  const original = fs.readFileSync(0, 'utf8');
  const parsed = ts.createSourceFile(input, original, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const names = [], namesToExport = [];
  for (const node of parsed.statements) {
    if (ts.isFunctionDeclaration(node) && node.name && node.body) {
      if (!node.name.text.startsWith('_')) {
        names.push(node.name.text);
        const exported = node.modifiers && node.modifiers.some(m => m.kind === ts.SyntaxKind.ExportKeyword);
        if (!exported) namesToExport.push(node.name.text);
      }
      continue;
    }
    if (ts.isInterfaceDeclaration(node) || ts.isTypeAliasDeclaration(node) ||
        ts.isEnumDeclaration(node) || ts.isEmptyStatement(node)) continue;
    fail('<bash++ typescript>:' + (parsed.getLineAndCharacterOfPosition(node.getStart(parsed)).line + 1) +
      ': only functions, interfaces, type aliases, and enums are allowed at module scope');
  }
  const source = original + (namesToExport.length ? '\nexport { ' + namesToExport.join(', ') + ' };\n' : '');
  const options = {target:ts.ScriptTarget.ES2022, module:ts.ModuleKind.CommonJS,
    moduleResolution:ts.ModuleResolutionKind.Node10, ignoreDeprecations:'6.0', skipLibCheck:true,
    noEmitOnError:true, strict:false, sourceMap:false, declaration:false};
  const host = ts.createCompilerHost(options);
  const baseGet = host.getSourceFile.bind(host);
  host.getSourceFile = (file, version, onError, fresh) =>
    file === input ? ts.createSourceFile(input, source, version, true, ts.ScriptKind.TS) : baseGet(file, version, onError, fresh);
  host.fileExists = ((base) => file => file === input || base(file))(host.fileExists.bind(host));
  host.readFile = ((base) => file => file === input ? source : base(file))(host.readFile.bind(host));
  let artifact = '';
  host.writeFile = (file, text) => { if (file.endsWith('.js')) artifact = text; };
  const program = ts.createProgram([input], options, host);
  const sourceFile = program.getSourceFile(input);
  const checker = program.getTypeChecker();
  const diagnostics = ts.getPreEmitDiagnostics(program);
  if (diagnostics.length) fail(diagnostics.map(d => diagnostic(ts, d)).join('\n'));
  const exports = [];
  const mapType = type => {
    if (type.flags & ts.TypeFlags.StringLike) return 'string';
    if (type.flags & ts.TypeFlags.NumberLike) return 'float64';
    if (type.flags & ts.TypeFlags.BooleanLike) return 'bool';
    if (type.flags & (ts.TypeFlags.Void | ts.TypeFlags.Undefined | ts.TypeFlags.Never)) return 'nil';
    const shown = checker.typeToString(type);
    if (shown === 'Uint8Array' || shown.startsWith('Uint8Array<')) return 'bytes';
    if (shown === 'any' || shown === 'unknown') return 'any';
    return '';
  };
  for (const node of sourceFile.statements) {
    if (!ts.isFunctionDeclaration(node) || !node.name || !node.body || node.name.text.startsWith('_')) continue;
    const sig = checker.getSignatureFromDeclaration(node);
    let dynamic = !sig;
    const params = [];
    if (sig) for (const symbol of sig.parameters) {
      const mapped = mapType(checker.getTypeOfSymbolAtLocation(symbol, node));
      params.push(mapped || 'any'); dynamic ||= !mapped || mapped === 'any';
    }
    let results = [];
    if (sig) {
      const mapped = mapType(checker.getReturnTypeOfSignature(sig));
      if (!mapped) dynamic = true;
      else if (mapped !== 'nil') results = [mapped];
    }
    exports.push({name:node.name.text,signature:{params,results,dynamic}});
  }
  const emitted = program.emit();
  if (emitted.emitSkipped || !artifact) fail('TypeScript compiler did not emit the module');
  process.stdout.write(JSON.stringify({ok:true,exports,artifact}));
} catch (error) {
  process.stderr.write(String(error && error.stack || error));
  process.exit(1);
}
`

const typeScriptAnalyze7 = `
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import cp from 'node:child_process';
import {pathToFileURL} from 'node:url';
const root = process.argv[1];
const apiModule = await import(pathToFileURL(path.join(root, 'dist/api/sync/api.js')));
const ast = await import(pathToFileURL(path.join(root, 'dist/ast/is.js')));
const original = fs.readFileSync(0, 'utf8');
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bashpp-typescript-'));
const input = path.join(dir, 'module.ts');
let api;
function fail(message) { process.stdout.write(JSON.stringify({ok:false,error:message})); process.exit(0); }
function location(sourceFile, node) {
  const p = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
  return '<bash++ typescript>:' + (p.line + 1) + ':' + (p.character + 1);
}
try {
  fs.writeFileSync(path.join(dir, 'tsconfig.json'), JSON.stringify({compilerOptions:{
    target:'es2022',module:'commonjs',outDir:'dist',strict:false,skipLibCheck:true,
    noEmitOnError:true,sourceMap:false,declaration:false},include:['module.ts']}));
  fs.writeFileSync(input, original);
  api = new apiModule.API({cwd:dir});
  let snapshot = api.updateSnapshot({openFiles:[input]});
  let project = snapshot.getDefaultProjectForFile(input);
  if (!project) fail('TypeScript 7 did not create a project for the fenced source');
  let sourceFile = project.program.getSourceFile(input);
  const names = [], namesToExport = [];
  for (const node of sourceFile.statements) {
    if (ast.isFunctionDeclaration(node) && node.name && node.body) {
      if (!node.name.text.startsWith('_')) {
        names.push(node.name.text);
        const exported = node.modifiers && node.modifiers.some(m => sourceFile.text.slice(m.pos,m.end).trim() === 'export');
        if (!exported) namesToExport.push(node.name.text);
      }
      continue;
    }
    if (ast.isInterfaceDeclaration(node) || ast.isTypeAliasDeclaration(node) ||
        ast.isEnumDeclaration(node) || ast.isEmptyStatement(node)) continue;
    fail(location(sourceFile,node) + ': only functions, interfaces, type aliases, and enums are allowed at module scope');
  }
  if (namesToExport.length) {
    fs.writeFileSync(input, original + '\nexport { ' + namesToExport.join(', ') + ' };\n');
    snapshot.dispose();
    snapshot = api.updateSnapshot({openFiles:[input]});
    project = snapshot.getDefaultProjectForFile(input);
    sourceFile = project.program.getSourceFile(input);
  }
  const diagnostics = [...project.program.getSyntacticDiagnostics(input), ...project.program.getSemanticDiagnostics(input), ...project.program.getProgramDiagnostics()];
  if (diagnostics.length) {
    fail(diagnostics.map(d => {
      if (!d.fileName || d.pos == null) return d.text;
      const file = project.program.getSourceFile(d.fileName);
      const p = file && file.getLineAndCharacterOfPosition(d.pos);
      return p ? '<bash++ typescript>:' + (p.line+1) + ':' + (p.character+1) + ': ' + d.text : d.text;
    }).join('\n'));
  }
  const checker = project.checker, exports = [];
  const mapType = type => {
    const shown = checker.typeToString(type);
    if (shown === 'string') return 'string';
    if (shown === 'number') return 'float64';
    if (shown === 'boolean' || shown === 'true' || shown === 'false') return 'bool';
    if (shown === 'void' || shown === 'undefined' || shown === 'never') return 'nil';
    if (shown === 'Uint8Array' || shown.startsWith('Uint8Array<')) return 'bytes';
    if (shown === 'any' || shown === 'unknown') return 'any';
    return '';
  };
  for (const node of sourceFile.statements) {
    if (!ast.isFunctionDeclaration(node) || !node.name || !node.body || node.name.text.startsWith('_')) continue;
    const type = checker.getTypeAtLocation(node.name);
    const sig = checker.getSignaturesOfType(type, apiModule.SignatureKind.Call)[0];
    let dynamic = !sig, params = [], results = [];
    if (sig) for (const symbol of sig.getParameters()) {
      const mapped = mapType(checker.getTypeOfSymbolAtLocation(symbol,node));
      params.push(mapped || 'any'); dynamic ||= !mapped || mapped === 'any';
    }
    if (sig) {
      const mapped = mapType(checker.getReturnTypeOfSignature(sig));
      if (!mapped) dynamic = true; else if (mapped !== 'nil') results = [mapped];
    }
    exports.push({name:node.name.text,signature:{params,results,dynamic}});
  }
  snapshot.dispose(); api.close(); api = undefined;
  const tsc = path.join(root, 'bin/tsc');
  const emitted = cp.spawnSync(process.execPath,[tsc,'-p',path.join(dir,'tsconfig.json'),'--pretty','false'],{encoding:'utf8'});
  if (emitted.status !== 0) fail((emitted.stdout || emitted.stderr || 'TypeScript 7 emit failed').trim());
  const artifact = fs.readFileSync(path.join(dir,'dist/module.js'),'utf8');
  process.stdout.write(JSON.stringify({ok:true,exports,artifact}));
} finally {
  if (api) api.close();
  fs.rmSync(dir,{recursive:true,force:true});
}
`

const typeScriptWorker = `
const readline = require('readline');
const vm = require('vm');
const util = require('util');
let moduleExports = Object.create(null), currentOut = '', currentErr = '';
const consoleBridge = {
  log: (...args) => { currentOut += util.format(...args) + '\n'; },
  info: (...args) => { currentOut += util.format(...args) + '\n'; },
  warn: (...args) => { currentErr += util.format(...args) + '\n'; },
  error: (...args) => { currentErr += util.format(...args) + '\n'; }
};
function dec(v) {
  if (v && typeof v === 'object' && Object.keys(v).length === 1 && '$bytes' in v) return Uint8Array.from(Buffer.from(v.$bytes, 'base64'));
  if (v && typeof v === 'object' && Object.keys(v).length === 1 && '$bigint' in v) return BigInt(v.$bigint);
  if (Array.isArray(v)) return v.map(dec);
  if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k,x]) => [k,dec(x)]));
  return v;
}
function enc(v) {
  if (v === undefined || v === null) return null;
  if (typeof v === 'bigint') return {$bigint:v.toString()};
  if (v instanceof Uint8Array) return {$bytes:Buffer.from(v).toString('base64')};
  if (Array.isArray(v)) return v.map(enc);
  if (v && Object.prototype.toString.call(v) === '[object Object]') return Object.fromEntries(Object.entries(v).map(([k,x]) => [k,enc(x)]));
  if (typeof v === 'number' && !Number.isFinite(v)) throw new TypeError('unsupported non-finite number result');
  if (typeof v === 'boolean' || typeof v === 'number' || typeof v === 'string') return v;
  throw new TypeError('unsupported foreign result type: ' + typeof v);
}
const rl = readline.createInterface({input:process.stdin, crlfDelay:Infinity});
rl.on('line', line => {
  let req, response;
  try {
    req = JSON.parse(line); const id = req.id || 0;
    if (req.op === 'load') {
      const context = vm.createContext({console:consoleBridge, Uint8Array, TextEncoder, TextDecoder});
      const module = {exports:{}};
      const wrapper = vm.runInContext('(function(exports,module){' + req.artifact + '\n})', context, {filename:'<bash++ typescript>.js'});
      wrapper(module.exports, module); moduleExports = module.exports;
      response = {id,ok:true};
    } else if (req.op === 'call') {
      currentOut = ''; currentErr = '';
      const fn = moduleExports[req.name];
      if (typeof fn !== 'function') throw new TypeError('unknown TypeScript export ' + req.name);
      const value = fn(...dec(req.args || []));
      if (value && typeof value.then === 'function') throw new TypeError('async TypeScript results are not supported');
      response = {id,ok:true,result:enc(value),stdout:currentOut,stderr:currentErr};
    } else throw new Error('unknown operation');
  } catch (error) {
    response = {id:req && req.id || 0,ok:false,error:String(error && error.stack || error),stdout:currentOut,stderr:currentErr};
  }
  process.stdout.write(JSON.stringify(response) + '\n');
});
`
