'use strict';

function readRecipeFile(text) {
  const recipe=JSON.parse(text);
  // JSON.parse keeps the last repeated property. The firmware rejects ambiguous
  // objects. Track keys in each object after the browser has checked the syntax.
  const keys=[];
  for(const match of text.matchAll(/("(?:\\.|[^"\\])*")\s*:|"(?:\\.|[^"\\])*"|[{}]/g)) {
    if(match[0]==='{')keys.push(new Set());
    else if(match[0]==='}')keys.pop();
    else if(match[1]) {
      const key=JSON.parse(match[1]), current=keys[keys.length-1];
      if(current.has(key))throw new Error('The recipe contains a repeated field: '+key+'.');
      current.add(key);
    }
  }
  // A revision belongs to the source brewer. An imported copy gets a new one.
  if(recipe && Object.hasOwn(recipe,'id') && Number.isInteger(recipe.revision) &&
     recipe.revision>0 && recipe.revision<=0xffffffff)delete recipe.revision;
  return recipe;
}

function recipeIssue(recipe) {
  const integer = (value,low,high) => Number.isInteger(value) && value>=low && value<=high;
  const label = (value,limit) => typeof value==='string' && value.length>0 && value.length<=limit && !/[^ -~]/.test(value);
  if (!recipe || !Array.isArray(recipe.steps) || typeof recipe.id!=='string' ||
      !recipe.id.length || recipe.id.length>20 || /[^a-zA-Z0-9_-]/.test(recipe.id))
    return 'Choose a Pico recipe with an ID of 1–20 letters, digits, hyphens or underscores.';
  if (!label(recipe.name,31)) return 'Use 1–31 printable ASCII characters for the recipe name.';
  if (Object.keys(recipe).some(key=>!['id','name','steps'].includes(key)))
    return 'This recipe includes unsupported fields.';
  if (!integer(recipe.steps.length,1,15)) return 'Use 1–15 brewing steps.';
  for (const [index, step] of recipe.steps.entries()) {
    const prefix='Step '+(index+1)+': ';
    if (!step || !label(step.name,21)) return prefix+'Use 1–21 printable ASCII characters for the name.';
    if (Object.keys(step).some(key=>!['name','location','target_f','minutes','drain_minutes'].includes(key)))
      return prefix+'This step includes unsupported fields.';
    if (!integer(step.location,1,6)) return prefix+'Choose a valid flow path.';
    if (step.target_f!==0 && !integer(step.target_f,60,212)) return prefix+'Use 0 °F for unheated circulation or a whole number from 60–212 °F.';
    if (!integer(step.minutes,0,599)) return prefix+'Use 0–599 whole minutes.';
    if ('drain_minutes' in step && !integer(step.drain_minutes,0,120)) return prefix+'Use 0–120 whole drain minutes.';
  }
  return '';
}

const flowPaths = ['Keg circulation', 'Mash', 'Hop addition 1', 'Hop addition 2', 'Hop addition 3', 'Hop addition 4'];

function addStep(step={name:'New step',location:1,target_f:152,minutes:20,drain_minutes:0}) {
  if ($('steps').children.length>=15) {notify('A recipe can contain up to 15 steps.',true);return;}
  const row=document.createElement('tr');
  const definitions=[['name','text',null,null],['location','select',1,6],['target_f','number',0,212],['minutes','number',0,599],['drain_minutes','number',0,120]];
  for (const [field,type,min,max] of definitions) {
    const cell=document.createElement('td');let input;
    if(type==='select') {input=document.createElement('select');flowPaths.forEach((label,index)=>input.add(new Option(label,String(index+1))));}
    else {input=document.createElement('input');input.type=type;if(type==='text'){input.maxLength=21;input.pattern='[ -~]+';input.title='Use printable ASCII characters';}else{input.min=min;input.max=max;input.step=1;}}
    input.dataset.field=field;input.required=true;input.value=step[field] ?? (field==='drain_minutes'?0:'');
    input.setAttribute('aria-label',field.replaceAll('_',' '));cell.append(input);row.append(cell);
  }
  const cell=document.createElement('td');const remove=document.createElement('button');remove.type='button';remove.className='remove';remove.textContent='×';remove.setAttribute('aria-label','Remove step');
  remove.addEventListener('click',()=>{row.remove();state.dirty=true;});cell.append(remove);row.append(cell);$('steps').append(row);
}

function editRecipe(recipe, existing=false) {
  state.editorId=existing ? recipe.id : null;
  state.editorRevision=existing ? recipe.revision : undefined;
  state.dirty=false;
  $('recipeName').value=recipe.name || '';$('recipeId').value=recipe.id || '';$('recipeId').readOnly=existing;
  $('recipeId').dataset.auto=existing ? 'false' : 'true';
  $('editorHeading').textContent=existing ? 'Edit recipe' : 'New recipe';$('steps').replaceChildren();
  for(const step of recipe.steps || [{name:'Mash',location:2,target_f:152,minutes:60,drain_minutes:0}])addStep(step);
  updateControls();
}

async function loadRecipe(id) {
  if(state.dirty && !confirm('Discard the unsaved recipe changes?'))return;
  try {const recipe=await api('/recipes/'+encodeURIComponent(id));editRecipe({...recipe,id},true);notify('');}
  catch(error){notify(error.message,true);}
}

function recipeFromForm() {
  if(!$('recipeForm').reportValidity())return null;
  const steps=Array.from($('steps').children).map(row=>{
    const step={};row.querySelectorAll('[data-field]').forEach(input=>step[input.dataset.field]=input.dataset.field==='name'?input.value:Number(input.value));return step;
  });
  const recipe={id:$('recipeId').value,name:$('recipeName').value,steps};
  const issue=recipeIssue(recipe);
  if(issue){notify(issue,true);return null;}
  return recipe;
}
