'use strict';

function updateControls() {
  const ready = state.connected && state.status?.ready === true;
  const busy = !!state.status?.busy;
  const stopped = !!state.status?.stopped;
  const idlePhase = state.status?.phase === 4;
  const advertised = command => state.capabilities.includes(command);
  const available = ready && !state.pendingCommands;
  document.querySelectorAll('[data-encoder]').forEach(button => button.disabled = !available || !advertised('encoder'));
  $('encoderClick').disabled = !available || !advertised('encoder');
  $('brewRecipe').disabled = !available || busy || stopped;
  $('startRecipe').disabled = !available || busy || stopped || !advertised('brew') || !$('brewRecipe').value;
  $('program').disabled = !available || busy || stopped;
  $('runProgram').disabled = !available || busy || stopped || !$('program').value;
  $('stopButton').disabled = !ready || stopped || state.stopPending || !advertised('stop');
  $('clearStop').hidden = !stopped;
  $('clearStop').disabled = !available || busy || !idlePhase || !advertised('clear-stop');
  $('machineControls').hidden = !state.restartSupported;
  $('restartBrewer').disabled = !available || busy || stopped || !idlePhase;
  $('newRecipe').disabled = !available || busy;
  $('restoreRecipes').disabled = !available || busy || !state.restoreDefaults;
  $('saveRecipe').disabled = !available || busy;
  $('deleteRecipe').disabled = !available || busy || !state.editorId;
  $('saveSettings').disabled = !available || busy;
  apiExplorer?.update();
  brewHistory?.update();
}


async function sendCommand(body) {
  const stop = body.command === 'stop';
  if (!state.connected || !state.capabilities.includes(body.command) ||
      (stop ? state.stopPending : state.pendingCommands)) return;
  const generation = state.generation;
  state.pendingCommands++;
  if (stop) state.stopPending = true;
  updateControls();
  try {
    const reply = await api('/commands','POST',body);
    if (reply?.accepted !== true || reply.command !== body.command)
      throw new Error('The brewer did not confirm this command.');
    if (body.command !== 'encoder') notify(stop ? 'Stop requested.' : 'Command accepted.');
    renderStatus(await api('/status'));
  } catch (error) {
    if (generation === state.generation) notify(error.message,true);
  } finally {
    if (generation === state.generation) {
      state.pendingCommands--;
      if (stop) state.stopPending = false;
      updateControls();
    }
  }
}

async function restartBrewer() {
  if ($('restartBrewer').disabled || !state.restartSupported ||
      !confirm('Restart the brewer now? It will briefly disconnect.')) return;
  const generation = state.generation;
  state.pendingCommands++;
  updateControls();
  try {
    const reply = await api('/system/restart','POST',{});
    if (reply?.accepted !== true) throw new Error('The brewer did not confirm the restart.');
    state.status = null;
    notify('Restart requested. Waiting for the brewer to reconnect.');
  } catch (error) {
    if (generation === state.generation) notify(error.message,true);
  } finally {
    if (generation === state.generation) {
      state.pendingCommands--;
      updateControls();
    }
  }
}
