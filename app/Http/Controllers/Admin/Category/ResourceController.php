<?php

namespace App\Http\Controllers\Admin\Category;

use App\Models\Category;
use App\Services\BaseListController;

class ResourceController extends BaseListController
{
    public $model = 'Category';

    public function index()
    {
        $this->authorize('index', $this->model_class);
        $this->meta['link_name'] = __(strtolower($this->model . '_create'));
        $this->meta['link_route'] = route('admin.' . $this->model_sm . '.list.create');
        $this->meta['search'] = 1;

        $columns = [];
        foreach(collect($this->model_columns)->where('table', true) as $column)
        {
            $columns[] = [
                'field' => $column['name'],
                'title' => preg_replace('/([a-z])([A-Z])/s', '$1 $2', \Str::studly($column['name'])),
            ];
        }
        $categories = $this->repository->orderBy('order', 'asc')->get()->toTree();

        return view('admin.list.tree-table', [
            'meta' => $this->meta,
            'columns' => $columns,
            'categories' => $categories,
        ]);
    }

    public function postTree()
    {
    	$tree_json = $this->request->categorytree;
        $tree = json_decode($tree_json);
    	$this->saveTree($tree, null);
    	$this->request->session()->flash('alert-success', $this->model . ' Order Updated Successfully!');

        return redirect()->route('admin.' . $this->model_sm . '.list.index');
    }

    public function saveTree($categories, $parent)
    {
        $order = 0;
        foreach($categories as $category)
        {
            $order ++;
            $node = Category::updateOrCreate(
                ['id' => $category->id],
                [
                    'order' => $order,
                ]
            );
            if(isset($parent)){
                $parent->appendNode($node);
            }
            if(isset($category->children)){
                $this->saveTree($category->children, $node);
            }
        }
    }

    public function getTree()
    {
    	return $this->repository->orderBy('order', 'asc')->get()->toTree();
    }
}
