-- Runs inside Envoy. One info line per request. On HTTP 500, the upstream
-- body (a Go stack trace) is written into Envoy's own log.

function envoy_on_request(handle)
  local headers = handle:headers()
  local path = headers:get(":path") or ""
  local method = headers:get(":method") or ""
  local meta = handle:streamInfo():dynamicMetadata()
  meta:set("envoy.filters.http.lua", "path", path)
  meta:set("envoy.filters.http.lua", "method", method)
  handle:logInfo(string.format("accepted %s %s", method, path))
end

function envoy_on_response(handle)
  local status = handle:headers():get(":status") or ""
  local meta = handle:streamInfo():dynamicMetadata():get("envoy.filters.http.lua")
  local path = ""
  local method = ""
  if meta ~= nil then
    path = meta["path"] or ""
    method = meta["method"] or ""
  end
  local code = tonumber(status) or 0
  if code >= 400 then
    local text = ""
    local body = handle:body(true)
    if body ~= nil and body:length() > 0 then
      text = body:getBytes(0, body:length())
    end
    local reason = failure_reason(status, text)
    if text == "" then
      text = "reason: " .. reason
    end
    handle:logErr(string.format("%s %s status=%s reason=%s\n%s", method, path, status, reason, text))
    return
  end
  handle:logDebug(string.format("completed %s %s status=%s", method, path, status))
end

function failure_reason(status, text)
  local first = (text or ""):match("^[^\n]*") or ""
  local tagged = first:match("^reason:%s*(.+)$")
  if tagged ~= nil and tagged ~= "" then
    return tagged
  end
  if status == "429" then
    return "rate limit exceeded"
  end
  if status == "503" then
    if first ~= "" then
      return first
    end
    return "upstream unavailable"
  end
  if status == "504" then
    return "upstream timeout"
  end
  if status == "500" then
    return "backend processing failed"
  end
  if first ~= "" and first:find("^goroutine") == nil then
    return first
  end
  return "request failed"
end
