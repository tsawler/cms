<?php

namespace App\Forms\Fields;

use Kris\LaravelFormBuilder\Fields\FormField;

class SwitchBootstrapM extends FormField
{
    /**
     * @inheritdoc
     */
    public function getDefaults()
    {
        return [
            'attr' => [
                'class' => null,
                'id' => $this->getName(),
                'data-switch' => 'true',
                'data-on-text' => 'Enabled',
                'data-off-text' => 'Disabled',
            ],
            'value' => 1,
            'checked' => true,
        ];
    }

    public function render(array $options = [], $showLabel = true, $showField = true, $showError = true)
    {
        $model = $this->parent->getModel();
        if($model){
            if($model[$this->name] === 0){
                $options['checked'] = false;
            }
        }

        $options['choices'] = ['on', 'off'];
        if($this->name === 'gender'){
            $options['choices'] = ['male', 'female'];
        }
        elseif($this->name === 'direction'){
            $options['choices'] = ['LTR', 'RTL'];
        }

        $options['attr']['data-on-text'] = $options['choices'][0];
        $options['attr']['data-off-text'] = $options['choices'][1];
        $options['attr']['data-on-color'] = 'success';


        return parent::render($options, $showLabel, $showField, $showError);
    }

    protected function getTemplate()
    {
        return 'vendor.laravel-form-builder.switch-bootstrap-m';
    }
}
